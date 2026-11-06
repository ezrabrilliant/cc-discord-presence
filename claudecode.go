package main

// claudeCodeHelper reports whether a claude process, given its arguments
// (the executable first), is a helper rather than a session: the Claude in
// Chrome native host, which runs for as long as Chrome is open, or
// claude mcp serve. stop.sh skips the same ones.
func claudeCodeHelper(args []string) bool {
	if len(args) < 2 {
		return false
	}
	if len(args) >= 3 && args[1] == "mcp" && args[2] == "serve" {
		return true
	}
	for _, arg := range args[1:] {
		if arg == "--chrome-native-host" {
			return true
		}
	}
	return false
}
