package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// A gRPC server with only grpcCompressUnary in front of two methods, one on
// the allowlist (ListItems) and one off it (GetItem).
func compressTestServer(t *testing.T) string {
	t.Helper()
	big := &pb.ListItemsResponse{}
	for i := 0; i < 200; i++ {
		big.Items = append(big.Items, &pb.Item{Id: fmt.Sprint("item-", i), Title: "A title about something " + fmt.Sprint(i),
			Summary: "A summary of a sentence or two, the length a feed actually carries.", Url: "https://example.com/" + fmt.Sprint(i)})
	}
	unary := func(resp proto.Message, newReq func() proto.Message) grpc.MethodHandler {
		return func(srv any, ctx context.Context, dec func(any) error, ic grpc.UnaryServerInterceptor) (any, error) {
			req := newReq()
			if err := dec(req); err != nil {
				return nil, err
			}
			h := func(context.Context, any) (any, error) { return resp, nil }
			return ic(ctx, req, &grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/" + map[bool]string{true: "ListItems", false: "GetItem"}[resp == proto.Message(big)]}, h)
		}
	}
	s := grpc.NewServer(grpc.UnaryInterceptor(grpcCompressUnary()))
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "articleflux.v1.ReaderService",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "ListItems", Handler: unary(big, func() proto.Message { return &pb.ListItemsRequest{} })},
			{MethodName: "GetItem", Handler: unary(&pb.GetItemResponse{Item: big.Items[0]}, func() proto.Message { return &pb.GetItemRequest{} })},
		},
	}, struct{}{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

// rawCall makes a gRPC call by hand over h2c, so the test controls exactly
// which grpc-accept-encoding is sent — including none, which is what every
// client cached before this build sends.
func rawCall(t *testing.T, addr, method, accept string) (encoding string, payload []byte) {
	t.Helper()
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	c := &http.Client{Transport: &http.Transport{Protocols: &p}}
	body := []byte{0, 0, 0, 0, 0} // uncompressed, zero-length message
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/articleflux.v1.ReaderService/"+method, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	if accept != "" {
		req.Header.Set("Grpc-Accept-Encoding", accept)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	all, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if st := resp.Trailer.Get("Grpc-Status"); st != "0" {
		t.Fatalf("grpc-status %q: %s", st, resp.Trailer.Get("Grpc-Message"))
	}
	if len(all) < 5 {
		t.Fatalf("short response %d", len(all))
	}
	n := binary.BigEndian.Uint32(all[1:5])
	if flag := all[0]; (flag == 1) != (resp.Header.Get("Grpc-Encoding") == "gzip") {
		t.Errorf("compressed flag %d but grpc-encoding %q", flag, resp.Header.Get("Grpc-Encoding"))
	}
	return resp.Header.Get("Grpc-Encoding"), all[5 : 5+n]
}

func TestGRPCResponseCompressionIsNegotiated(t *testing.T) {
	addr := compressTestServer(t)
	for _, tc := range []struct {
		method, accept, want string
	}{
		{"ListItems", "", ""},         // an old cached client: identity, as before
		{"ListItems", "identity", ""}, // says it takes nothing
		{"ListItems", "gzip", "gzip"}, // the new client
		{"ListItems", "identity,gzip", "gzip"},
		{"GetItem", "gzip", ""}, // off the allowlist: never compressed
	} {
		enc, payload := rawCall(t, addr, tc.method, tc.accept)
		if enc != tc.want {
			t.Errorf("%s with grpc-accept-encoding %q: grpc-encoding %q, want %q", tc.method, tc.accept, enc, tc.want)
		}
		if enc == "" {
			if err := proto.Unmarshal(payload, &pb.ListItemsResponse{}); err != nil && tc.method == "ListItems" {
				t.Errorf("identity payload does not decode: %v", err)
			}
		}
	}
}

// The real client, with the codec registered as client/data now does, gets
// the same message back whether or not it was compressed on the wire.
func TestGRPCCompressedResponseRoundTrips(t *testing.T) {
	addr := compressTestServer(t)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	got := &pb.ListItemsResponse{}
	if err := conn.Invoke(context.Background(), "/articleflux.v1.ReaderService/ListItems", &pb.ListItemsRequest{}, got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 200 || got.Items[199].GetId() != "item-199" {
		t.Errorf("got %d items", len(got.Items))
	}
	_, wire := rawCall(t, addr, "ListItems", "gzip")
	_, plain := rawCall(t, addr, "ListItems", "")
	t.Logf("200-item ListItems on the wire: identity %d bytes, gzip %d bytes", len(plain), len(wire))
	if len(wire) >= len(plain) {
		t.Errorf("gzip %d is not smaller than identity %d", len(wire), len(plain))
	}
}
