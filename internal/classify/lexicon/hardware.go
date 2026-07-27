package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// hardware is the Hardware & Chips category (plan.md §27.3d #3).
//
// `apple` and `amazon` are both guarded here for the same shape of reason as
// everywhere else they appear: unguarded, "Apple picking season" and "the
// Amazon is burning" both read as hardware. The guard list is the company's
// device line, not the company name itself, because the device line is what
// actually distinguishes a hardware story from a business or climate one.
func hardware() classify.Label {
	return classify.Label{
		Slug: "hardware",
		Name: "Hardware & Chips",
		Terms: []classify.Term{
			{Text: "tsmc", Weight: 2.4},
			{Text: "snapdragon", Weight: 2.2},
			{Text: "vision pro", Weight: 2.0},
			{Text: "risc-v", Weight: 2.0},
			{Text: "npu", Weight: 2.0},
			{Text: "silicon wafer", Weight: 2.0},
			{Text: "teardown", Weight: 2.0},
			{Text: "chip shortage", Weight: 1.9},
			{Text: "ryzen", Weight: 1.9},
			{Text: "nanometer", Weight: 1.8},
			{Text: "overclock", Weight: 1.8},
			{Text: "thermal throttling", Weight: 1.8},
			{Text: "semiconductor", Weight: 1.8},
			{Text: "motherboard", Weight: 1.8},
			{Text: "raspberry pi", Weight: 1.8},
			{Text: "esp32", Weight: 1.7},
			{Text: "arduino", Weight: 1.7},
			{Text: "iphone", Weight: 1.6},
			{Text: "macbook", Weight: 1.6},
			{Text: "airpods", Weight: 1.6},
			{Text: "ipad", Weight: 1.6},
			{Text: "graphics card", Weight: 1.6},
			{Text: "qualcomm", Weight: 1.6},
			{Text: "mediatek", Weight: 1.6},
			{Text: "samsung galaxy", Weight: 1.6},
			{Text: "foldable phone", Weight: 1.6},
			{Text: "google pixel", Weight: 1.6},
			{Text: "chipset", Weight: 1.5},
			{Text: "mechanical keyboard", Weight: 1.4},
			{Text: "bios", Weight: 1.4},
			{Text: "refresh rate", Weight: 1.4},
			{Text: "megapixel", Weight: 1.4},
			{Text: "kindle", Weight: 1.4},
			{Text: "amazon echo", Weight: 1.4},
			{Text: "ring doorbell", Weight: 1.4},
			{Text: "fire tv", Weight: 1.4},
			{Text: "firmware", Weight: 1.3},
			{Text: "chipmaker", Weight: 1.3},
			{Text: "oled", Weight: 1.2},
			{Text: "display panel", Weight: 1.2},
			{Text: "camera sensor", Weight: 1.2},
			{Text: "processor", Weight: 1.1},
			{Text: "alexa", Weight: 1.1},
			{Text: "cpu", Weight: 1.1},
			{Text: "gpu", Weight: 1.1},
			{Text: "amd", Weight: 1.1},
			{Text: "nvidia", Weight: 1.1},
			{Text: "smartwatch", Weight: 1.1},
			{Text: "charging speed", Weight: 1.1},
			{Text: "battery life", Weight: 1.0},
			{Text: "ssd", Weight: 1.0},
			{Text: "usb-c", Weight: 1.0},
			{Text: "wearable", Weight: 0.9},
			{Text: "earbuds", Weight: 0.9},
			{Text: "drone", Weight: 0.8},
			{Text: "intel", Weight: 0.8},
			{Text: "benchmark", Weight: 0.7},
			{Text: "laptop", Weight: 0.7},
			{Text: "smartphone", Weight: 0.7},
			{Text: "ram", Weight: 0.7},
			{Text: "headphones", Weight: 0.6},
			{Text: "monitor", Weight: 0.5},
			{Text: "android", Weight: 0.5},
			{Text: "apple", Weight: 1.0, Requires: []string{
				"iphone", "ipad", "mac", "ios", "macos", "app store",
				"cupertino", "tim cook", "vision pro", "airpods",
			}},
			{Text: "amazon", Weight: 0.7, Requires: []string{
				"kindle", "echo", "alexa", "ring", "fire tv", "amazon devices", "echo dot",
			}},

			// Consumer-device vocabulary (§27.11 round 1): the shipped list was
			// written from general knowledge and never named the words a
			// phone/gadget review or launch article actually uses. Kept at the
			// low end of the band deliberately — each is common enough on its
			// own to also show up in a business or culture story, so the
			// weight is what keeps one bare mention from outscoring an
			// article's real topic.
			{Text: "phone", Weight: 0.5},
			{Text: "camera", Weight: 0.5},
			{Text: "display", Weight: 0.5},
			{Text: "devices", Weight: 0.5},
			{Text: "battery", Weight: 0.5},
			{Text: "5g", Weight: 0.6},
			{Text: "foldable", Weight: 0.8},
			{Text: "smart home", Weight: 0.9},
			{Text: "launch price", Weight: 1.0},
			{Text: "fast charging", Weight: 1.0},
			{Text: "wireless charging", Weight: 1.0},
			{Text: "smart speaker", Weight: 1.1},
			{Text: "oppo", Weight: 1.1},
			{Text: "vivo", Weight: 1.1},
			{Text: "realme", Weight: 1.1},
			{Text: "curved display", Weight: 1.2},
			{Text: "xiaomi", Weight: 1.2},
			{Text: "oneplus", Weight: 1.2},
			{Text: "huawei", Weight: 1.2},
			{Text: "budget phone", Weight: 1.2},
			{Text: "flagship phone", Weight: 1.3},
			// `galaxy` is Samsung's product line, not the astronomical object —
			// space.go never claims the bare word, but an unguarded "galaxy"
			// would still fire on genuine astronomy coverage ("a distant
			// galaxy", "galaxy cluster"), so it needs the same guard shape as
			// `apple`/`amazon` above.
			{Text: "galaxy", Weight: 1.3, Requires: []string{
				"samsung", "unpacked", "galaxy s", "galaxy z", "galaxy tab",
				"galaxy watch", "one ui",
			}},
			{Text: "samsung", Weight: 0.6},
			{Text: "hardware", Weight: 0.6},
			{Text: "cameras", Weight: 0.5},
			// InfoComm is the pro-AV/broadcast-hardware trade show (PTZ
			// cameras, LED walls, switchers) a dedicated slice of this feed
			// covers every year; specific enough not to need a guard.
			{Text: "infocomm", Weight: 1.2},
			{Text: "power station", Weight: 1.1},
			{Text: "webcam", Weight: 0.9},
			{Text: "fitness tracker", Weight: 1.3},
			{Text: "smart glasses", Weight: 1.2},
			{Text: "mirrorless", Weight: 1.0},
			{Text: "garmin", Weight: 1.2},
			{Text: "redmi", Weight: 1.2},
			{Text: "subwoofer", Weight: 1.0},
			{Text: "hdmi", Weight: 0.9},
			{Text: "usb", Weight: 0.5},
			{Text: "smart tv", Weight: 1.0},
			{Text: "router", Weight: 0.6},
			{Text: "rtx", Weight: 1.3},
			{Text: "canon", Weight: 1.1},
			{Text: "panasonic", Weight: 1.1},
			{Text: "gpus", Weight: 1.1},
			// Computex is Taipei's flagship PC/component trade show, the same
			// shape of unambiguous proper noun as InfoComm above.
			{Text: "computex", Weight: 1.2},
			{Text: "lenovo", Weight: 1.2},
			{Text: "projector", Weight: 0.9},
		},
		Exclude: []classify.Term{
			{Text: "apple orchard", Weight: 2.5},
			{Text: "apple picking", Weight: 2.5},
			{Text: "apple pie", Weight: 2.0},
			{Text: "rainforest", Weight: 2.0},
		},
		MinScore: 0,
		Prompt: "Assign for consumer and industrial hardware: chips, devices, components, " +
			"teardowns, benchmarks. Not for the software running on the device, and not for a " +
			"chipmaker's earnings or stock (see Business & Companies).",
	}
}
