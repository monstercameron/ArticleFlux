package obs

import (
	"sync"
	"testing"
	"time"
)

func TestTunnelsConnectedDisconnectedCounts(t *testing.T) {
	var tn Tunnels
	tn.Connected()
	tn.Connected()
	s := tn.Stats()
	if s.Live != 2 || s.Total != 2 || s.Peak != 2 {
		t.Fatalf("after 2 connects: %+v, want Live=2 Total=2 Peak=2", s)
	}
	tn.Disconnected()
	s = tn.Stats()
	if s.Live != 1 || s.Total != 2 || s.Peak != 2 {
		t.Fatalf("after 1 disconnect: %+v, want Live=1 Total=2 Peak=2", s)
	}
}

// TestTunnelsPeakTracksHighWaterMark: Peak must record the maximum
// concurrent count ever seen, even after connections later drop off.
func TestTunnelsPeakTracksHighWaterMark(t *testing.T) {
	var tn Tunnels
	tn.Connected()
	tn.Connected()
	tn.Connected() // live=3, peak=3
	tn.Disconnected()
	tn.Disconnected() // live=1
	tn.Connected()    // live=2, total=4

	s := tn.Stats()
	if s.Peak != 3 {
		t.Errorf("Peak = %d, want 3 (the high-water mark, not the current count)", s.Peak)
	}
	if s.Live != 2 {
		t.Errorf("Live = %d, want 2", s.Live)
	}
	if s.Total != 4 {
		t.Errorf("Total = %d, want 4 (every Connected() call, ever)", s.Total)
	}
}

// TestTunnelsDisconnectedFloorsAtZero: a stray/extra Disconnected() call —
// which can happen on a defer path racing a Connected() failure — must not
// take Live negative.
func TestTunnelsDisconnectedFloorsAtZero(t *testing.T) {
	var tn Tunnels
	tn.Disconnected()
	tn.Disconnected()
	s := tn.Stats()
	if s.Live != 0 {
		t.Fatalf("Live = %d after Disconnected() with nothing connected, want 0 (must not go negative)", s.Live)
	}
}

func TestTunnelsSinceIsZeroBeforeAnyConnection(t *testing.T) {
	var tn Tunnels
	s := tn.Stats()
	if s.Since != 0 {
		t.Errorf("Since = %v before any Connected(), want 0", s.Since)
	}
	if s.SinceLastDrop != 0 {
		t.Errorf("SinceLastDrop = %v before any Disconnected(), want 0", s.SinceLastDrop)
	}
}

func TestTunnelsSinceAndSinceLastDropAdvance(t *testing.T) {
	var tn Tunnels
	tn.Connected()
	time.Sleep(5 * time.Millisecond)
	s := tn.Stats()
	if s.Since <= 0 {
		t.Errorf("Since = %v after Connected(), want > 0", s.Since)
	}
	if s.SinceLastDrop != 0 {
		t.Errorf("SinceLastDrop = %v with no disconnect yet, want 0", s.SinceLastDrop)
	}

	tn.Disconnected()
	time.Sleep(5 * time.Millisecond)
	s = tn.Stats()
	if s.SinceLastDrop <= 0 {
		t.Errorf("SinceLastDrop = %v after Disconnected(), want > 0", s.SinceLastDrop)
	}
}

func TestTunnelsConcurrentAccessRace(t *testing.T) {
	var tn Tunnels
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tn.Connected()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tn.Disconnected()
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = tn.Stats()
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Sanity: Live must never have gone negative in a way that survives —
	// the floor-at-zero guard means it should still be representable.
	s := tn.Stats()
	if s.Live < 0 {
		t.Fatalf("Live went negative under concurrent access: %d", s.Live)
	}
}
