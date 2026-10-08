package goblinname

// firstNames are the names a goblin can be given: short, friendly human
// first names, each one word of letters.
var firstNames = []string{
	"Abe", "Alfie", "Archie", "Arlo", "Barney", "Bea", "Benny", "Bernie", "Biff", "Bix",
	"Bruno", "Carla", "Clem", "Cora", "Cosmo", "Cyril", "Daisy", "Dex", "Dina", "Dolly",
	"Dot", "Duke", "Edna", "Elsie", "Enzo", "Ernie", "Etta", "Faye", "Fern", "Fitz",
	"Flo", "Frank", "Frida", "Fritz", "Gabe", "Gertie", "Gil", "Goldie", "Gordy", "Greta",
	"Gus", "Hank", "Hattie", "Hazel", "Hector", "Herb", "Huey", "Hugo", "Ida", "Iggy",
	"Ike", "Inez", "Ingrid", "Iris", "Ivy", "Jasper", "Jerry", "Jojo", "Jonas", "Jules",
	"June", "Kenny", "Kiki", "Kip", "Kit", "Kurt", "Lars", "Lenny", "Leroy", "Lola",
	"Lou", "Lyle", "Mabel", "Maggie", "Marv", "Midge", "Milo", "Mo", "Mona", "Murray",
	"Ned", "Nell", "Nico", "Nina", "Noel", "Norm", "Odie", "Olive", "Ollie", "Oona",
	"Opal", "Oscar", "Otis", "Pablo", "Pat", "Pearl", "Penny", "Percy", "Phil", "Pip",
	"Quincy", "Ralph", "Reggie", "Rex", "Rita", "Rocco", "Rosie", "Ruby", "Rufus", "Sadie",
	"Sal", "Shirley", "Sid", "Sonny", "Stan", "Suki", "Tad", "Teddy", "Tess", "Tilly",
	"Toby", "Trudy", "Ugo", "Uma", "Ursula", "Val", "Vera", "Vern", "Vi", "Vic",
	"Vince", "Wally", "Walt", "Wanda", "Wendell", "Wes", "Wilma", "Winnie", "Woody", "Yara",
	"Yuri", "Yvette", "Zach", "Zane", "Zeke", "Zora",
}

// genericTitles are the titles for work no theme fits: playful and simple.
var genericTitles = []string{
	"Code Designer", "Bit Shepherd", "Loop Tamer", "Branch Whisperer", "Commit Crafter",
	"Logic Gardener", "Patch Pilot", "Code Sculptor", "Syntax Surfer", "Byte Barista",
	"Refactor Ranger", "Function Fixer", "Script Wizard", "Module Mechanic", "Variable Wrangler",
	"Diff Detective", "Idea Juggler", "Chief Tinkerer", "Bracket Balancer", "Null Ninja",
	"Stack Stacker", "Code Chef", "Logic Locksmith", "Type Tamer", "Code Carpenter",
	"Line Tidier", "Byte Builder", "Spec Reader", "Detail Hound", "Plan Maker",
	"Code Whittler", "Bit Twiddler", "Puzzle Solver", "Gear Turner", "Thread Weaver",
	"Module Maker", "Config Keeper", "Tidy Coder", "Code Plumber", "Bit Painter",
	"Edge Case Chaser", "Loose End Tier", "Brace Wrangler", "Commit Poet", "Loop Juggler",
	"Idea Sprinkler", "Code Cobbler", "Logic Knitter", "Byte Polisher", "Chief Fiddler",
}

// theme is a kind of work a brief can make obvious: the words that name it
// and the titles that fit it.
type theme struct {
	key    string
	words  []string
	titles []string
}

// themes are tried in order, so the first of two themes a hint names
// equally often wins.
var themes = []theme{
	{key: "board", words: []string{"board", "card", "cards", "canvas", "panel", "panels", "tooltip", "tooltips", "frontend", "ui", "css", "layout", "screen", "screens", "pixel", "pixels", "icon", "icons", "design", "page", "pages", "window", "button", "buttons", "theme", "react"},
		titles: []string{"Pixel Wrangler", "Layout Whisperer", "Button Polisher", "Card Shuffler", "Color Mixer", "Screen Painter"}},
	{key: "bugs", words: []string{"test", "tests", "flaky", "flake", "flakes", "bug", "bugs", "crash", "crashes", "hang", "hangs", "race", "regression", "broken", "failing", "debug"},
		titles: []string{"Bug Hunter", "Flake Catcher", "Crash Detective", "Glitch Tamer", "Gremlin Chaser", "Bug Squasher"}},
	{key: "docs", words: []string{"doc", "docs", "readme", "documentation", "copy", "wording", "writing", "guide", "prose"},
		titles: []string{"Word Smith", "Page Turner", "Ink Slinger", "Story Teller"}},
	{key: "delivery", words: []string{"merge", "train", "ci", "pipeline", "gate", "release", "releases", "deploy", "ship", "landing"},
		titles: []string{"Train Conductor", "Pipeline Plumber", "Release Wrangler", "Merge Maestro", "Ship Captain"}},
	{key: "keys", words: []string{"auth", "credential", "credentials", "secret", "secrets", "token", "tokens", "security", "login", "password"},
		titles: []string{"Key Keeper", "Lock Smith", "Gate Keeper", "Vault Guard"}},
	{key: "install", words: []string{"install", "installer", "update", "upgrade", "build", "binary", "setup", "package"},
		titles: []string{"Bolt Tightener", "Toolbox Keeper", "Crate Packer", "Wrench Wielder"}},
	{key: "terminal", words: []string{"terminal", "terminals", "console", "shell", "pane", "panes", "powershell", "conpty", "keyboard", "cursor"},
		titles: []string{"Shell Tamer", "Terminal Tinkerer", "Prompt Whisperer", "Cursor Herder"}},
	{key: "voice", words: []string{"voice", "dictation", "speech", "audio", "microphone"},
		titles: []string{"Voice Catcher", "Echo Chaser", "Sound Shaper"}},
	{key: "speed", words: []string{"memory", "disk", "perf", "performance", "speed", "fast", "slow", "cache", "caches", "latency"},
		titles: []string{"Speed Tuner", "Byte Saver", "Memory Keeper", "Turbo Tuner"}},
	{key: "data", words: []string{"database", "migration", "migrations", "sql", "schema", "postgres", "supabase", "query", "queries"},
		titles: []string{"Data Herder", "Table Setter", "Query Wrangler", "Row Counter"}},
}
