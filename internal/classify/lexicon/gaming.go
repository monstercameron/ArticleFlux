package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// gaming is the Gaming category (plan.md §27.3d #16).
//
// `patch notes` is the deliberate winner of the collision with security's
// `patch`: it is left unguarded and above 2.0 so that a game update always
// beats a guarded, lower-weight `patch` in security.go, per §27.3d's own
// note on this row. `valve` and `mod` both have a common non-gaming reading
// (a mechanical valve; a subculture or moderator) and are guarded.
func gaming() classify.Label {
	return classify.Label{
		Slug: "gaming",
		Name: "Gaming",
		Terms: []classify.Term{
			{Text: "speedrun", Weight: 2.6},
			{Text: "patch notes", Weight: 2.4},
			{Text: "nintendo switch", Weight: 2.2},
			{Text: "playstation", Weight: 2.0},
			{Text: "xbox", Weight: 2.0},
			{Text: "esports", Weight: 2.0},
			{Text: "indie game", Weight: 2.0},
			{Text: "loot box", Weight: 2.0},
			{Text: "battle pass", Weight: 2.0},
			{Text: "game jam", Weight: 2.0},
			{Text: "fortnite", Weight: 2.0},
			{Text: "minecraft", Weight: 2.0},
			{Text: "roblox", Weight: 2.0},
			{Text: "call of duty", Weight: 2.0},
			{Text: "league of legends", Weight: 2.0},
			{Text: "world of warcraft", Weight: 2.0},
			{Text: "battle royale", Weight: 2.0},
			{Text: "microtransaction", Weight: 2.0},
			{Text: "review bombing", Weight: 2.0},
			{Text: "ps5", Weight: 2.0},
			{Text: "xbox series x", Weight: 2.0},
			{Text: "unreal engine", Weight: 2.0},
			{Text: "unity engine", Weight: 2.0},
			{Text: "gaming tournament", Weight: 1.8},
			{Text: "dlc", Weight: 1.8},
			{Text: "season pass", Weight: 1.8},
			{Text: "early access", Weight: 1.8},
			{Text: "boss fight", Weight: 1.8},
			{Text: "retro gaming", Weight: 1.8},
			{Text: "twitch stream", Weight: 1.8},
			{Text: "handheld console", Weight: 1.6},
			{Text: "matchmaking", Weight: 1.6},
			{Text: "procedurally generated", Weight: 1.8},
			{Text: "first-person shooter", Weight: 1.8},
			{Text: "cloud gaming", Weight: 1.8},
			{Text: "game pass", Weight: 1.8},
			{Text: "epic games", Weight: 1.8},
			{Text: "crunch time", Weight: 1.8},
			{Text: "game engine", Weight: 1.6},
			{Text: "game trailer", Weight: 1.6},
			{Text: "game studio", Weight: 1.4},
			{Text: "leaderboard", Weight: 1.4},
			{Text: "vr headset", Weight: 1.6},
			{Text: "pixel art", Weight: 1.6},
			{Text: "open world", Weight: 1.6},
			{Text: "game delay", Weight: 1.4},
			{Text: "steam", Weight: 1.6},
			{Text: "nintendo", Weight: 1.6},
			{Text: "video game", Weight: 1.4},
			{Text: "game developer", Weight: 1.2},
			{Text: "multiplayer", Weight: 1.2},
			{Text: "single-player", Weight: 1.2},
			{Text: "co-op", Weight: 1.2},
			{Text: "console", Weight: 1.0},
			{Text: "beta test", Weight: 1.0},
			{Text: "rpg", Weight: 1.2},
			{Text: "virtual reality", Weight: 1.0},
			{Text: "achievements", Weight: 0.9},
			{Text: "gamer", Weight: 0.7},
			{Text: "streamer", Weight: 0.7},
			{Text: "publisher", Weight: 0.5},
			{Text: "ranked", Weight: 0.6},
			{Text: "arcade", Weight: 0.8},
			{Text: "valve", Weight: 1.2, Requires: []string{
				"steam", "game", "half-life", "portal", "counter-strike", "game developer",
			}},
			{Text: "mod", Weight: 0.9, Requires: []string{
				"game", "steam workshop", "modding", "patch", "nexus mods", "community",
			}},
		},
		Exclude: []classify.Term{
			{Text: "pressure valve", Weight: 2.0},
			{Text: "safety valve", Weight: 2.0},
			{Text: "heart valve", Weight: 2.0},
			{Text: "mod squad", Weight: 1.5},
		},
		MinScore: 0,
		Prompt: "Assign for video games and the industry making them: titles, platforms, esports, " +
			"development. Not for gambling regulation, and not for a game engine used for non-game " +
			"visualization work (see Software & Development).",
	}
}
