// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"path"
	"regexp"
	"strings"
)

// CommandPrograms names the programs a shell command invoked, and keeps nothing
// else about it.
//
// It exists because "Bash" is the most-used tool on most machines and says
// nothing: running the tests, committing, grepping the tree and starting a
// container are one bar. The programs are the variety inside it.
//
// What is kept is a VOCABULARY, in the same sense tool names are: `git`, `go`,
// `rg`, `make`. What is discarded is everything that makes a command that
// member's command — the arguments, the paths, the flags, the here-docs, the
// URLs. A shell line is much closer to something the member wrote than a tool
// name is, and a log of them would be the transcript by instalments that
// corrections.go refuses. So the text is read, reduced to a handful of names,
// and dropped.
//
// Chains count as what they are: `go build && go test` is two calls of go,
// `cat x | rg y` is one cat and one rg, because the member did both things.
func CommandPrograms(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || len(cmd) > 8000 {
		// A command that long is a here-doc or a generated payload; whatever is
		// at the front of it is not a summary of what it did.
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, seg := range splitSegments(cmd) {
		if p := programOf(seg); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// maxSegments bounds how much of one command line is read. A chain longer than
// this is not better summarised by reading all of it.
const maxSegments = 24

// splitSegments breaks a command where one program ends and the next begins —
// and, the part that matters, refuses to break inside anything that is DATA
// rather than command.
//
// It used to be one regular expression over `|`, `&&`, `;` and the newline,
// which is right for a chain and wrong for everything a newline appears inside.
// A here-doc body is the member's own text — a script, a JSON payload, a
// message — and reading its lines as commands is how `import`, `EOF`, `An` and
// `Three` came to be recorded as programs somebody ran. A quoted string
// spanning lines does the same on a smaller scale, and a `|` inside either one
// invents a program out of the middle of a sentence.
//
// So this scans rather than matches: quotes suspend every separator, a here-doc
// opener makes the whole body invisible, and `>` is still not a separator
// because sending output to a file does not run anything.
func splitSegments(cmd string) []string {
	var out []string
	var seg strings.Builder
	var pending []string // here-doc delimiters whose bodies have not begun
	var quote byte
	flush := func() {
		if s := strings.TrimSpace(seg.String()); s != "" {
			out = append(out, s)
		}
		seg.Reset()
	}
	for i := 0; i < len(cmd) && len(out) < maxSegments; {
		c := cmd[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(cmd) {
				i += 2 // an escape inside double quotes hides the next byte
				continue
			}
			if c == quote {
				quote = 0
			}
			seg.WriteByte(c)
			i++
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
			seg.WriteByte(c)
			i++
		case c == '\\' && i+1 < len(cmd):
			// An escaped byte, the line continuation included: neither is a
			// separator and neither starts a program.
			seg.WriteByte(c)
			seg.WriteByte(cmd[i+1])
			i += 2
		case c == '<' && i+1 < len(cmd) && cmd[i+1] == '<':
			d, n := heredocOpener(cmd[i:])
			if d != "" {
				pending = append(pending, d)
			}
			seg.WriteString(cmd[i : i+n])
			i += n
		case c == '#' && wordStart(cmd, i):
			// A comment runs to the end of its line, and everything in it is
			// prose. A `;` inside one used to start a new "command": the line
			// "# the relay restarts it; start it directly" filed `start` as a
			// program somebody ran.
			for i < len(cmd) && cmd[i] != '\n' {
				i++
			}
		case c == '\n':
			flush()
			i++
			// Whatever the line just ended opened is data until its delimiter.
			for len(pending) > 0 {
				i = skipHeredoc(cmd, i, pending[0])
				pending = pending[1:]
			}
		case c == ';':
			flush()
			i++
		case c == '|':
			flush()
			i++
			if i < len(cmd) && cmd[i] == '|' {
				i++
			}
		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&':
			flush()
			i += 2
		default:
			// A lone `&` backgrounds a command and is not a separator, which is
			// also what keeps `2>&1` in one piece.
			seg.WriteByte(c)
			i++
		}
	}
	if len(out) < maxSegments {
		flush()
	}
	return out
}

// wordStart reports whether the byte at i begins a word rather than sitting
// inside one, which is the difference between a comment and a URL fragment or
// an id like `head-3`.
func wordStart(s string, i int) bool {
	if i == 0 {
		return true
	}
	switch s[i-1] {
	case ' ', '\t', '\n', ';', '|', '&', '(':
		return true
	}
	return false
}

// heredocDelim matches the word a here-doc is terminated by, quoted or not.
var heredocDelim = regexp.MustCompile(`^<<-?\s*(?:'([A-Za-z_][A-Za-z0-9_]*)'|"([A-Za-z_][A-Za-z0-9_]*)"|([A-Za-z_][A-Za-z0-9_]*))`)

// heredocOpener reads a here-doc opener at the front of s, returning the
// delimiter and how many bytes the opener took. `<<<` is a here-string: its
// data is on the same line and there is no body to skip, so it opens nothing.
func heredocOpener(s string) (string, int) {
	if strings.HasPrefix(s, "<<<") {
		return "", 3
	}
	m := heredocDelim.FindStringSubmatch(s)
	if m == nil {
		return "", 2
	}
	for _, g := range m[1:] {
		if g != "" {
			return g, len(m[0])
		}
	}
	return "", len(m[0])
}

// skipHeredoc walks from the start of a here-doc body to the byte after its
// terminator, or to the end when the body never closes — an unterminated body
// is still data, and reading the rest of it as commands is the bug this exists
// to stop.
func skipHeredoc(cmd string, i int, delim string) int {
	for i < len(cmd) {
		end := strings.IndexByte(cmd[i:], '\n')
		line := cmd[i:]
		next := len(cmd)
		if end >= 0 {
			line = cmd[i : i+end]
			next = i + end + 1
		}
		if strings.TrimSpace(line) == delim {
			return next
		}
		i = next
	}
	return i
}

// programToken is what a program name may look like. Anything else — a path
// with a slash in it, a variable, a quoted string, a flag — is not a name and
// is not kept.
var programToken = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._+-]{0,31}$`)

// wrappers run something else and are not themselves the act. Skipping them
// gets to the program that did the work, which is what the member would name.
var wrappers = map[string]bool{
	"sudo": true, "env": true, "nohup": true, "time": true, "command": true,
	"exec": true, "xargs": true, "nice": true, "timeout": true, "doas": true,
}

// shellWords are syntax, not programs. `for` and `if` open a construct; the
// program inside it is found in a later segment or not at all. The builtins
// below are here for the same reason: `break` ends a loop and `read` takes a
// line from a pipe, and a bar called "break" beside git reads as a program
// somebody installed. Words that ARE also real programs — echo, test, printf,
// kill — stay off this list, because running one is running something.
var shellWords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "function": true, "in": true, "return": true,
	"true": true, "false": true, "cd": true, "export": true, "local": true,
	"set": true, "unset": true, "shift": true, "eval": true, "source": true,
	"break": true, "continue": true, "exit": true, "wait": true, "trap": true,
	"read": true, "declare": true, "readonly": true, "let": true, "alias": true,
	"pushd": true, "popd": true, "jobs": true, "fg": true, "bg": true,
}

// tokenRole is what one word of a segment contributes. The two readers below
// walk the same words, so the rules for reading them live here once: two
// implementations of "is this a program" is how `2>/dev/null` came to be filed
// as a program called null in one of them.
type tokenRole int

const (
	roleSkip tokenRole = iota // not a program, and the next word still might be
	roleName                  // a candidate program name
	roleHalt                  // this segment does not start with a command at all
)

// classifyToken reads one word. skipNext is set by a bare redirection operator,
// whose following word names a file rather than a program.
func classifyToken(tok string) (name string, role tokenRole, skipNext bool) {
	tok = strings.TrimLeft(tok, "(){ \t")
	switch {
	case tok == "":
		return "", roleSkip, false
	case strings.ContainsAny(tok, "<>"):
		// A redirection names a file, and a file is not a program: `2>/dev/null`
		// is not `null` and `> /tmp/page.html` is not `page.html`.
		return "", roleSkip, strings.HasSuffix(tok, ">") || strings.HasSuffix(tok, "<")
	case strings.HasPrefix(tok, "-"):
		return "", roleSkip, false
	case strings.Contains(tok, "=") && !strings.HasPrefix(tok, "-"):
		// `FOO=bar make deploy` prefixes a command, so the program is the next
		// word. `PORT=$(grep …` IS the command, and everything after it on the
		// segment is that command's arguments — which is how a path argument
		// came to be read as a program called agent.env.
		if sub := substitution(tok); sub != "" {
			tok = sub
			break
		}
		return "", roleSkip, false
	}
	if sub := substitution(tok); sub != "" {
		tok = sub
	}
	name = path.Base(strings.Trim(tok, "();"))
	if !programToken.MatchString(name) || shellWords[name] {
		return "", roleHalt, false
	}
	return name, roleName, false
}

// substitution returns the command inside `$(…)` or a backtick, which is a
// command the member ran however it was written down.
func substitution(tok string) string {
	if i := strings.LastIndex(tok, "$("); i >= 0 {
		return tok[i+2:]
	}
	if i := strings.LastIndex(tok, "`"); i >= 0 {
		return tok[i+1:]
	}
	return ""
}

func programOf(segment string) string {
	skip := false
	for _, tok := range strings.Fields(segment) {
		if skip {
			skip = false
			continue
		}
		name, role, skipNext := classifyToken(tok)
		skip = skipNext
		switch role {
		case roleSkip:
			continue
		case roleHalt:
			return ""
		}
		if wrappers[name] {
			continue // the real program is the next word
		}
		return name
	}
	return ""
}

// subcommandPrograms are the programs whose second word is a FIXED vocabulary —
// git has commit and push, go has test and build — so keeping it says what the
// member did without saying anything about them.
//
// The list is deliberately short and closed. `psql mydatabase` and `ssh
// prod-box-7` have a second word too, and it is theirs; a general rule that kept
// the second word of anything would collect names, hosts and databases. So a
// program earns its subcommand by being on this list, and everything else is
// recorded as the program alone.
var subcommandPrograms = map[string]bool{
	"git": true, "gh": true, "go": true, "cargo": true, "npm": true,
	"yarn": true, "pnpm": true, "docker": true, "kubectl": true, "helm": true,
	"make": true, "systemctl": true, "brew": true, "apt": true, "apt-get": true,
	"pip": true, "pip3": true, "uv": true, "terraform": true, "tailscale": true,
}

// subcommandToken is what a subcommand may look like: a short lowercase word.
// Anything with a slash, a dot, an uppercase letter or a digit-heavy shape is
// more likely a path, a host or an identifier than a verb.
var subcommandToken = regexp.MustCompile(`^[a-z][a-z0-9-]{0,20}$`)

// CommandDetail is CommandPrograms one level finer: the program, and its
// subcommand where the program has a closed vocabulary of them. "git commit"
// rather than "git", so a drill-down can say what the version control actually
// was — committing, pushing, or reading the log.
func CommandDetail(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || len(cmd) > 8000 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, seg := range splitSegments(cmd) {
		d := detailOf(seg)
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func detailOf(segment string) string {
	prog := ""
	skip := false
	for _, tok := range strings.Fields(segment) {
		if skip {
			skip = false
			continue
		}
		name, role, skipNext := classifyToken(tok)
		skip = skipNext
		if role == roleSkip {
			// A flag before any subcommand means the program is the answer.
			if prog != "" && strings.HasPrefix(strings.TrimLeft(tok, "(){ \t"), "-") {
				return prog
			}
			continue
		}
		if prog == "" {
			if role == roleHalt {
				return ""
			}
			if wrappers[name] {
				continue
			}
			prog = name
			if !subcommandPrograms[prog] {
				return prog
			}
			continue
		}
		// The word after a program that has subcommands. Kept only where the
		// program has a CLOSED vocabulary of them: psql's second word is a
		// database and ssh's is a host.
		clean := strings.TrimLeft(tok, "(){ \t")
		if subcommandToken.MatchString(clean) && !strings.Contains(clean, "/") {
			return prog + " " + clean
		}
		return prog
	}
	return prog
}

// ProgramOf returns the program half of a detail key, so a view can group
// "git commit" and "git push" under git without splitting strings itself.
func ProgramOf(detail string) string {
	prog, _, _ := strings.Cut(detail, " ")
	return prog
}

// ProgramKinds is the order the kinds are shown in, and ProgramKind puts a
// program in one. Same rule as ToolKind: an unrecognised name is "other" rather
// than a guess, because the long tail of what people run is unbounded and a
// wrong heading reads as a measured one.
var ProgramKinds = []string{"vcs", "build", "test", "package", "search", "files", "network", "container", "data", "other"}

var programKinds = map[string]string{
	"git": "vcs", "gh": "vcs", "hg": "vcs", "svn": "vcs", "jj": "vcs",

	"make": "build", "go": "build", "cargo": "build", "gradle": "build",
	"mvn": "build", "tsc": "build", "webpack": "build", "vite": "build",
	"gcc": "build", "clang": "build", "cmake": "build", "bazel": "build",

	"pytest": "test", "jest": "test", "vitest": "test", "phpunit": "test",
	"rspec": "test", "playwright": "test", "gotestsum": "test",

	"npm": "package", "yarn": "package", "pnpm": "package", "pip": "package",
	"pip3": "package", "uv": "package", "poetry": "package", "brew": "package",
	"apt": "package", "apt-get": "package", "dnf": "package", "gem": "package",
	"bundle": "package", "asdf": "package", "nix": "package",

	"rg": "search", "grep": "search", "egrep": "search", "ag": "search",
	"find": "search", "fd": "search", "locate": "search", "ack": "search",

	"ls": "files", "cat": "files", "head": "files", "tail": "files",
	"cp": "files", "mv": "files", "rm": "files", "mkdir": "files",
	"touch": "files", "chmod": "files", "sed": "files", "awk": "files",
	"wc": "files", "sort": "files", "uniq": "files", "diff": "files",
	"tar": "files", "zip": "files", "unzip": "files", "stat": "files",

	"curl": "network", "wget": "network", "ssh": "network", "scp": "network",
	"rsync": "network", "dig": "network", "ping": "network", "nc": "network",
	"tailscale": "network", "fuser": "network", "ss": "network",

	"docker": "container", "podman": "container", "kubectl": "container",
	"helm": "container", "systemctl": "container", "compose": "container",

	"psql": "data", "mysql": "data", "sqlite3": "data", "redis-cli": "data",
	"jq": "data", "python": "data", "python3": "data", "node": "data",
}

func ProgramKind(prog string) string {
	if k, ok := programKinds[prog]; ok {
		return k
	}
	return "other"
}

// ProgramKindLabel is what a kind is called in front of a member.
func ProgramKindLabel(kind string) string {
	switch kind {
	case "vcs":
		return "Version control"
	case "build":
		return "Building"
	case "test":
		return "Testing"
	case "package":
		return "Packages"
	case "search":
		return "Searching"
	case "files":
		return "Files"
	case "network":
		return "Network"
	case "container":
		return "Containers & services"
	case "data":
		return "Data & scripting"
	}
	return "Other"
}

// pathFields are where the harnesses put the file a tool acted on.
var pathFields = []string{"file_path", "path", "notebook_path", "filePath", "target_file"}

// extToken bounds what counts as an extension: a short, lowercase, alphanumeric
// suffix. It is a vocabulary — .go, .md, .css — and the eight-character cap is
// what keeps it one: a longer suffix is more likely a name than a type, and is
// reported as "(none)" rather than kept. Nothing in an extension carries a
// directory or a secret, which is why it is kept and the path is not.
var extToken = regexp.MustCompile(`^\.[a-z0-9]{1,8}$`)

// PathExt is the file extension a tool acted on, lowercased, or "" when the
// input names no file or the suffix is not one. A file with no extension is
// reported as "(none)" rather than dropped, because "most of what you edit has
// no extension" is itself an answer.
func PathExt(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, f := range pathFields {
		p, _ := m[f].(string)
		if p == "" {
			continue
		}
		ext := strings.ToLower(path.Ext(path.Base(p)))
		if ext == "" {
			return "(none)"
		}
		if extToken.MatchString(ext) {
			return ext
		}
		return "(none)"
	}
	return ""
}

// PathTool reports whether a tool acts on a named file, so its detail is the
// kind of file rather than a program.
func PathTool(name string) bool {
	switch name {
	case "Read", "Edit", "Write", "NotebookEdit", "NotebookRead", "MultiEdit", "read_file":
		return true
	}
	return false
}

// ShellTool reports whether a tool's input carries a shell command worth
// reducing. The names are the ones the supported harnesses use for "run
// something"; anything else has no command to read.
func ShellTool(name string) bool {
	switch name {
	case "Bash", "BashOutput", "shell", "run_terminal_cmd", "execute_command":
		return true
	}
	return false
}

// --- what a tool keeps behind its name ---

// Detail kinds name the VOCABULARY a tool's second level is made of. A tool
// with no kind keeps nothing behind its name, and that is the common case: most
// tool inputs are what the member typed — a prompt, a URL, a regular
// expression, a search phrase — and none of those is a vocabulary. The rule for
// what may be kept is the one CommandPrograms states: an enumerable set of
// names the session demonstrably used, never the text that makes it theirs.
const (
	DetailProgram  = "program"  // a shell tool: the programs it ran
	DetailFileType = "filetype" // a file or search tool: the kind of file
	DetailAgent    = "agent"    // a delegation: which agent it handed to
	DetailSkill    = "skill"    // a skill or slash command: which one
)

// ToolDetailKind names the vocabulary a tool keeps, or "" for one that keeps
// none. It lives here, beside the extraction, because two readers need the same
// answer — the log, to record, and the view, to label a heading and to say
// which tools open at all rather than leaving a member to guess from which
// rows happen to be links.
func ToolDetailKind(name string) string {
	switch {
	case ShellTool(name):
		return DetailProgram
	case PathTool(name), SearchTool(name):
		return DetailFileType
	case DelegateTool(name):
		return DetailAgent
	case SkillTool(name):
		return DetailSkill
	}
	return ""
}

// ToolDetail reduces one tool call to the keys its second level counts. Empty
// for a tool that keeps none, and empty for a call that named none — a grep
// with no file type is not a grep of "(none)", it is a call this level cannot
// speak for, and the view says how many of those there were.
func ToolDetail(name string, input any) []string {
	switch ToolDetailKind(name) {
	case DetailProgram:
		return CommandDetail(CommandOf(input))
	case DetailFileType:
		if SearchTool(name) {
			if ext := globExt(input, searchGlobFields[name]); ext != "" {
				return []string{ext}
			}
			return nil
		}
		if ext := PathExt(input); ext != "" {
			return []string{ext}
		}
	case DetailAgent:
		if n := fieldName(input, agentFields); n != "" {
			return []string{n}
		}
	case DetailSkill:
		if n := fieldName(input, skillFields); n != "" {
			return []string{n}
		}
	}
	return nil
}

// SearchTool reports whether a tool searches the tree, where the file type it
// was pointed at is a vocabulary and the pattern it was given is not. The
// pattern is the member's own text — the one thing a search must never keep —
// so only the glob fields below are ever read.
func SearchTool(name string) bool {
	_, ok := searchGlobFields[name]
	return ok
}

// searchGlobFields is where each search tool puts a file glob. Named per tool
// rather than as one list, because the same field name means different things:
// Glob's `pattern` is a glob and Grep's `pattern` is a regular expression, and
// a shared list would read one member's search text into the log.
var searchGlobFields = map[string][]string{
	"Grep":        {"glob"},
	"Glob":        {"pattern", "glob"},
	"file_search": {"pattern", "glob"},
}

// DelegateTool reports whether a tool hands work to another agent, whose TYPE
// is a vocabulary — the agents this member has installed. Workflow is a
// delegation too and is deliberately absent: what it carries is a script, which
// is text the member wrote.
func DelegateTool(name string) bool {
	switch name {
	case "Task", "Agent":
		return true
	}
	return false
}

// SkillTool reports whether a tool invokes a named skill or slash command. The
// name is a vocabulary — what is installed — and the arguments beside it are
// not, so only the first word is ever read.
func SkillTool(name string) bool {
	switch name {
	case "Skill", "SlashCommand":
		return true
	}
	return false
}

var agentFields = []string{"subagent_type", "agent_type", "agent"}
var skillFields = []string{"skill", "command"}

// nameToken bounds what counts as a name: the shape an agent type, a skill or a
// slash command has. Anything longer or stranger is not a name — it is a
// sentence someone typed — and is dropped rather than kept.
var nameToken = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:+-]{0,47}$`)

// fieldName reads the first named field that holds a name, keeping the first
// word only: "/code-review ultra 42" is the command `code-review`, and what
// followed it is the member's own text.
func fieldName(input any, fields []string) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, f := range fields {
		v, _ := m[f].(string)
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		v = strings.TrimPrefix(strings.Fields(v)[0], "/")
		if nameToken.MatchString(v) {
			return v
		}
		return ""
	}
	return ""
}

// globExt is the file type a search named, or "" when it named none. Unlike
// PathExt there is no "(none)": a glob that names a whole filename is not a
// type, and a search that named no glob is a call this level cannot speak for.
func globExt(input any, fields []string) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, f := range fields {
		g, _ := m[f].(string)
		if g == "" {
			continue
		}
		ext := strings.ToLower(path.Ext(path.Base(g)))
		if extToken.MatchString(ext) {
			return ext
		}
	}
	return ""
}

// CommandOf pulls the command line out of a tool input, which harnesses spell
// two ways: a string, or the argv array a shell would be handed.
func CommandOf(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	switch c := m["command"].(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		// argv form is usually ["bash","-lc","the actual line"], and the line is
		// the part worth reading.
		if len(parts) > 0 && (parts[0] == "bash" || parts[0] == "sh" || parts[0] == "zsh") {
			return parts[len(parts)-1]
		}
		return strings.Join(parts, " ")
	}
	return ""
}
