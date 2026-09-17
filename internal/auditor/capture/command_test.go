// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"reflect"
	"strings"
	"testing"
)

// Bash is the most-used tool on most machines and says nothing on its own. The
// programs are the variety inside it — and the programs are ALL that is kept:
// no arguments, no paths, no flags, no here-docs.
func TestCommandProgramsKeepsOnlyTheNames(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		want      []string
	}{
		{"plain", "git status", []string{"git"}},
		{"chain counts both", "go build ./... && go test ./...", []string{"go"}},
		{"pipeline counts both", "cat notes.md | rg secret", []string{"cat", "rg"}},
		{"absolute path is basenamed", "/usr/local/bin/git push", []string{"git"}},
		{"sudo is not the act", "sudo systemctl restart tacit", []string{"systemctl"}},
		{"env prefix is skipped", "FOO=bar BAZ=1 make deploy", []string{"make"}},
		{"mixed separators", "ls; rg todo | wc -l", []string{"ls", "rg", "wc"}},
		{"duplicates collapse", "go build && go vet && go test", []string{"go"}},
	} {
		if got := CommandPrograms(tc.cmd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: CommandPrograms(%q) = %v, want %v", tc.name, tc.cmd, got, tc.want)
		}
	}
}

// The whole point is that nothing identifying survives. Anything that is not a
// bare program name — a URL, a path, a quoted string, a variable, a flag — must
// not come out the other side.
func TestCommandProgramsLeaksNothingIdentifying(t *testing.T) {
	for _, cmd := range []string{
		`curl -X POST https://api.internal.example/v1/customers/8837 -d '{"name":"Dana"}'`,
		`psql -c "SELECT email FROM users WHERE id = 42"`,
		`git commit -m "fix the thing for Acme Corp"`,
		`rg "password=hunter2" /home/dana/secrets`,
		`echo $SECRET_TOKEN | base64`,
	} {
		for _, got := range CommandPrograms(cmd) {
			if !programToken.MatchString(got) {
				t.Errorf("CommandPrograms(%q) returned %q, which is not a bare name", cmd, got)
			}
			for _, leak := range []string{"Dana", "email", "Acme", "hunter2", "dana", "customers", "https"} {
				if got == leak {
					t.Errorf("CommandPrograms(%q) leaked %q", cmd, got)
				}
			}
		}
	}
	// A here-doc or a generated payload is not summarised by whatever is at the
	// front of it, so a very long line is skipped entirely.
	long := "cat <<'EOF'\n" + string(make([]byte, 9000)) + "\nEOF"
	if got := CommandPrograms(long); got != nil {
		t.Errorf("a %d-byte command returned %v; it should be skipped", len(long), got)
	}
}

// Shell syntax is not a program: `if` and `for` open a construct and run
// nothing, and counting them would put a bar called "if" beside git.
func TestShellSyntaxIsNotAProgram(t *testing.T) {
	for _, cmd := range []string{"if test -f x", "for f in *.go", "cd /tmp", "export PATH=/x",
		// Builtins that end or steer a construct rather than run anything. A
		// bar called "break" beside git reads as a program somebody installed.
		"break", "continue", "exit 1", "wait", "read -r line"} {
		if got := CommandPrograms(cmd); len(got) != 0 {
			t.Errorf("CommandPrograms(%q) = %v, want none", cmd, got)
		}
	}
}

// Harnesses spell the input two ways: a string, or the argv a shell is handed.
func TestCommandOfReadsBothInputShapes(t *testing.T) {
	if got := CommandOf(map[string]any{"command": "git status"}); got != "git status" {
		t.Errorf("string form = %q", got)
	}
	argv := map[string]any{"command": []any{"bash", "-lc", "go test ./..."}}
	if got := CommandOf(argv); got != "go test ./..." {
		t.Errorf("argv form = %q, want the line bash was handed", got)
	}
	if got := CommandOf("not a map"); got != "" {
		t.Errorf("a non-map input returned %q", got)
	}
}

func TestProgramKindsAreLabelled(t *testing.T) {
	if ProgramKind("git") != "vcs" || ProgramKind("rg") != "search" || ProgramKind("docker") != "container" {
		t.Error("a known program landed in the wrong kind")
	}
	if ProgramKind("some-internal-script") != "other" {
		t.Error("an unknown program should be other, not a guess")
	}
	for _, k := range ProgramKinds {
		if ProgramKindLabel(k) == "" {
			t.Errorf("kind %q has no label", k)
		}
	}
}

// The second level: a subcommand, but only where the program has a CLOSED
// vocabulary of them. git has commit and push; psql's second word is a database
// and ssh's is a host, and a general rule would collect both.
func TestSubcommandsOnlyWhereTheVocabularyIsClosed(t *testing.T) {
	for _, tc := range []struct{ cmd, want string }{
		{"git commit -m 'x'", "git commit"},
		{"go test ./...", "go test"},
		{"make deploy", "make deploy"},
		{"docker build -t x .", "docker build"},
		// Not on the list: the program alone, whatever follows it.
		{"psql mydatabase", "psql"},
		{"ssh prod-box-7", "ssh"},
		{"curl https://api.example/customers/8837", "curl"},
		// On the list, but the next word is not a verb.
		{"git /some/path", "git"},
		{"go -v", "go"},
		{"make", "make"},
	} {
		got := CommandDetail(tc.cmd)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("CommandDetail(%q) = %v, want [%q]", tc.cmd, got, tc.want)
		}
	}
	if got := ProgramOf("git commit"); got != "git" {
		t.Errorf("ProgramOf = %q", got)
	}
}

// A file tool's second level is the KIND of file. The extension is a
// vocabulary; the path it came from is not, and none of it is kept.
func TestPathExtKeepsTheKindNotThePath(t *testing.T) {
	for _, tc := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"file_path": "/home/dana/work/project/internal/x.go"}, ".go"},
		{map[string]any{"path": "docs/design/NOTES.MD"}, ".md"},
		{map[string]any{"file_path": "/etc/hosts"}, "(none)"},
		// An unfamiliar suffix is still a file KIND, and case is normalised.
		{map[string]any{"file_path": "/home/dana/.ssh/id_rsa.KEYFILE"}, ".keyfile"},
		// Too long to be an extension: more likely a name than a type.
		{map[string]any{"file_path": "report.AcmeCorpQ3Figures"}, "(none)"},
		{map[string]any{}, ""},
	} {
		if got := PathExt(tc.in); got != tc.want {
			t.Errorf("PathExt(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Nothing from a path may survive except a short lowercase suffix.
	for _, p := range []string{"/home/dana/customers/acme-corp/secret.go", "C:/Users/Dana/x.md"} {
		got := PathExt(map[string]any{"file_path": p})
		if got != "" && !extToken.MatchString(got) && got != "(none)" {
			t.Errorf("PathExt(%q) = %q, which is not an extension", p, got)
		}
		for _, leak := range []string{"dana", "Dana", "acme", "customers", "secret"} {
			if strings.Contains(strings.ToLower(got), leak) {
				t.Errorf("PathExt(%q) leaked %q", p, got)
			}
		}
	}
}

// A shell tool was the only tool with a second level, which made Bash the only
// row on the tools page that opened — not because Bash is special but because
// it was the only vocabulary anyone had written down. Three more inputs carry
// one: a search points at a kind of file, a delegation names an agent, a skill
// call names a skill. Each is a set of names the session demonstrably used,
// which is the same standard the programs meet.
func TestToolDetailReadsEveryVocabularyThereIs(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		input map[string]any
		kind  string
		want  []string
	}{
		{"Bash", map[string]any{"command": "go test ./..."}, DetailProgram, []string{"go test"}},
		{"Read", map[string]any{"file_path": "/home/dana/work/project/usage.go"}, DetailFileType, []string{".go"}},
		{"Grep", map[string]any{"pattern": "func (s *Server)", "glob": "**/*.go"}, DetailFileType, []string{".go"}},
		{"Glob", map[string]any{"pattern": "internal/**/*.css"}, DetailFileType, []string{".css"}},
		{"Task", map[string]any{"subagent_type": "Explore", "prompt": "find where the key is"}, DetailAgent, []string{"Explore"}},
		{"Skill", map[string]any{"skill": "tacit:review"}, DetailSkill, []string{"tacit:review"}},
		{"SlashCommand", map[string]any{"command": "/code-review ultra 481"}, DetailSkill, []string{"code-review"}},
	} {
		if got := ToolDetailKind(tc.tool); got != tc.kind {
			t.Errorf("%s keeps %q, want %q", tc.tool, got, tc.kind)
		}
		if got := ToolDetail(tc.tool, tc.input); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ToolDetail(%s) = %v, want %v", tc.tool, got, tc.want)
		}
	}
	// A tool whose input is what the member typed keeps nothing, and says so
	// with an empty kind rather than by quietly returning no keys — the view
	// reads the kind to tell "nothing to open" from "nothing this window".
	for _, tool := range []string{"WebFetch", "WebSearch", "TodoWrite", "mcp__tacit__tacit_search", "Workflow"} {
		if got := ToolDetailKind(tool); got != "" {
			t.Errorf("%s claims a second level (%q) it cannot honestly keep", tool, got)
		}
		if got := ToolDetail(tool, map[string]any{"url": "https://internal.example/customers/8837",
			"query": "how do I rotate the key", "prompt": "the customer is Dana"}); got != nil {
			t.Errorf("ToolDetail(%s) = %v, want nothing", tool, got)
		}
	}
}

// The new vocabularies are held to the line the programs are: a name, and
// nothing that makes it this member's. A search's regular expression is their
// text, an agent prompt is their text, and a slash command's arguments are
// their text — none of it may reach the log through the field beside it.
func TestNewVocabulariesLeakNoFreeText(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		input map[string]any
	}{
		{"Grep", map[string]any{"pattern": "password=hunter2", "path": "/home/dana/secrets"}},
		{"Grep", map[string]any{"pattern": "Dana", "glob": "customers/dana-8837.json"}},
		{"Glob", map[string]any{"pattern": "/home/dana/work/acme-secret-thing/**"}},
		{"Task", map[string]any{"subagent_type": "review the merger memo for Acme", "prompt": "x"}},
		{"Task", map[string]any{"description": "email Dana about the invoice"}},
		{"Skill", map[string]any{"skill": "", "args": "the customer is Dana at dana@example.com"}},
		{"SlashCommand", map[string]any{"command": "/remember Dana's key is hunter2"}},
	} {
		for _, got := range ToolDetail(tc.tool, tc.input) {
			for _, leak := range []string{"Dana", "hunter2", "dana", "Acme", "acme", "example.com", "8837", "invoice", "merger"} {
				if strings.Contains(got, leak) {
					t.Fatalf("ToolDetail(%s, %v) returned %q, which carries %q", tc.tool, tc.input, got, leak)
				}
			}
			if len(got) > 48 {
				t.Errorf("ToolDetail(%s) returned a %d-character key: %q", tc.tool, len(got), got)
			}
		}
	}
	// A glob that names one file names no type, and a search that named no
	// glob is a call this level cannot speak for. Neither is "(none)": that
	// answer belongs to a file tool, where "most of what I edit has no
	// extension" is a real reading.
	if got := ToolDetail("Grep", map[string]any{"pattern": "func main"}); got != nil {
		t.Errorf("a search with no glob returned %v", got)
	}
	if got := ToolDetail("Glob", map[string]any{"pattern": "**/Makefile"}); got != nil {
		t.Errorf("a glob naming a filename returned %v", got)
	}
}

// A here-doc body is not a list of commands. It is a script, a payload, a
// message — the member's own text, arriving through the one tool that carries
// text — and splitting a command on every newline read each of its lines as
// something that ran. The tools page showed the result: `import`, `EOF`, `An`
// and `Three` listed as programs beside git and go, with nothing to say they
// were sentences.
func TestHereDocBodiesAreNotCommands(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		want      []string
	}{
		{"python here-doc", "python3 - <<'PY'\nimport json\nprint('An answer')\nPY\necho done",
			[]string{"python3", "echo"}},
		{"unquoted delimiter", "cat > f.md <<EOF\n- Three things; and more\nEOF", []string{"cat"}},
		{"body holds separators", "cat <<'EOF'\nls | grep secret && rm -rf /\nEOF\ngit status",
			[]string{"cat", "git"}},
		{"tab-stripped delimiter", "cat <<-TXT | wc -l\n\tbody\n\tTXT\nls", []string{"cat", "wc", "ls"}},
		{"never closed", "cat <<'EOF'\nimport this\nprint(that)\n", []string{"cat"}},
		{"here-string has no body", "echo hi <<< 'data; with | separators'\nls", []string{"echo", "ls"}},
	} {
		if got := CommandPrograms(tc.cmd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: CommandPrograms = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The same rule one scale down: a quoted string is data wherever it sits, so a
// commit message, a remote command or a JSON payload contributes no program —
// however many newlines, pipes and semicolons it holds.
func TestQuotedTextIsNotACommand(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want []string
	}{
		{"git commit -m \"line one\n\nline two | three\"", []string{"git"}},
		{"ssh host 'systemctl restart x; journalctl -n 5'", []string{"ssh"}},
		{`curl -d '{"note":"ls; rm -rf /"}' https://x/y`, []string{"curl"}},
		// Redirection is still not a separator, and a lone & backgrounds rather
		// than separating — which is also what keeps 2>&1 in one piece.
		{"go test ./... 2>&1 | head -5", []string{"go", "head"}},
		{"make deploy &", []string{"make"}},
	} {
		if got := CommandPrograms(tc.cmd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("CommandPrograms(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

// The same mistake as the here-doc, one word at a time: something that is not
// the start of a command being read as one. A redirection names a file, a
// comment is prose, and an assignment from a command substitution IS the
// command — so its arguments are arguments, not the program. Each of these was
// filed as a program somebody ran: `null` from 2>/dev/null, `agent.env` from a
// path argument, `start` from the second half of a comment.
func TestArgumentsAreNotPrograms(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		want      []string
	}{
		{"redirection to a device", "curl -s https://x/ | head -5 2>/dev/null", []string{"curl", "head"}},
		{"redirection to a file", "echo hi > out.txt", []string{"echo"}},
		{"spaced redirection", "cat page > /tmp/live-usage.html", []string{"cat"}},
		{"assignment from a substitution", `PORT=$(grep -o "X" ~/.config/tacit/agent.env | cut -d= -f2)`,
			[]string{"grep", "cut"}},
		{"substitution keeps the real program", `PID=$(systemctl --user show -p MainPID --value x); echo "$PID"`,
			[]string{"systemctl", "echo"}},
		{"comment runs to the line end", "# the relay restarts it; start it directly\nls", []string{"ls"}},
		{"trailing comment", "ls # list them; cat nothing", []string{"ls"}},
		// And the shapes that must keep working: an env prefix still prefixes a
		// command, a redirection still does not separate one, and a path still
		// resolves to the program at the end of it.
		{"env prefix", "FOO=bar BAZ=1 make deploy", []string{"make"}},
		{"stderr redirect is not a separator", "go test ./... 2>&1 | head -5", []string{"go", "head"}},
		{"absolute path", "/usr/local/bin/git push", []string{"git"}},
	} {
		if got := CommandPrograms(tc.cmd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: CommandPrograms(%q) = %v, want %v", tc.name, tc.cmd, got, tc.want)
		}
	}
	// The subcommand survives all of it, which is the half of the detail a
	// member actually reads.
	if got := CommandDetail("go test ./... 2>&1 | head -5"); !reflect.DeepEqual(got, []string{"go test", "head"}) {
		t.Errorf("CommandDetail lost the subcommand: %v", got)
	}
}
