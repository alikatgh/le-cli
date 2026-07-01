package intel

import "testing"

// parseDockerPorts and brewFormula parse `docker ps` output and process
// command lines respectively — both external, both outside this process's
// control. Must never panic on malformed input.

func FuzzParseDockerPorts(f *testing.F) {
	f.Add("abc123\tmy-container\t0.0.0.0:3000->3000/tcp\n")
	f.Add("")
	f.Add("no tabs here at all")
	f.Add("id\tname\t\n")
	f.Add("a\tb\tc\td\te\n")
	f.Add("abc\tsame-port\t0.0.0.0:3000->3000/tcp\ndef\tsame-port-2\t0.0.0.0:3000->3000/tcp\n")
	f.Fuzz(func(t *testing.T, out string) {
		parseDockerPorts(out)
	})
}

func FuzzBrewFormula(f *testing.F) {
	f.Add("/opt/homebrew/Cellar/redis/7.2.0/bin/redis-server")
	f.Add("/usr/local/opt/mongodb-community/bin/mongod")
	f.Add("")
	f.Add("/cellar/")
	f.Add("/opt/homebrew/Cellar/homebrew/bin/x")
	f.Add("/opt/homebrew/Cellar/redis@7/7.2.0/bin/redis-server")
	f.Fuzz(func(t *testing.T, cmd string) {
		brewFormula(cmd)
	})
}

func FuzzWordMatch(f *testing.F) {
	f.Add("hello world", "world")
	f.Add("", "")
	f.Add("nodeapp", "node")
	f.Fuzz(func(t *testing.T, haystack, needle string) {
		wordMatch(haystack, needle)
	})
}
