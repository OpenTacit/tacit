// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/markdown"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// docNode is one level of the docs tree: the documents at this level plus
// the named subdirectories beneath it.
type docNode struct {
	docs []docEntry
	dirs map[string]*docNode
}

// docsTree folds the flat doc list into a tree by slug path. Slugs are already
// sorted, so each node's docs stay in slug order.
func docsTree(docs []docEntry) *docNode {
	root := &docNode{dirs: map[string]*docNode{}}
	for _, d := range docs {
		node, parts := root, strings.Split(d.slug, "/")
		for _, dir := range parts[:len(parts)-1] {
			child := node.dirs[dir]
			if child == nil {
				child = &docNode{dirs: map[string]*docNode{}}
				node.dirs[dir] = child
			}
			node = child
		}
		node.docs = append(node.docs, d)
	}
	return root
}

// searchKeys returns everything filterable under a node — used so a directory
// row hides only when none of its descendants match the header filter.
func (n *docNode) searchKeys() string {
	var b strings.Builder
	for _, d := range n.docs {
		b.WriteString(strings.ToLower(d.slug + " " + d.title + " "))
	}
	for _, child := range n.dirs {
		b.WriteString(child.searchKeys())
	}
	return b.String()
}

// isDocIndex reports whether a doc is a folder's index.md — the folder's own
// description, rendered on the folder page rather than listed as a leaf.
func isDocIndex(slug string) bool {
	return slug == "index" || strings.HasSuffix(slug, "/index")
}

// findDocsNode walks the tree to the node for a directory path ("" = root).
func findDocsNode(root *docNode, dir string) *docNode {
	n := root
	if dir == "" {
		return n
	}
	for _, part := range strings.Split(dir, "/") {
		if n = n.dirs[part]; n == nil {
			return nil
		}
	}
	return n
}

// docsFolderHTML renders a folder page: the folder's index.md — its own
// explanation of what lives here — above a tile grid of its immediate
// contents. Subdirectories are category tiles (their index's title and
// summary); documents are page tiles (title and first paragraph). Deeper
// levels are reached through their category tile or the contents menu. The
// docs index ("" = the root folder) and every subdirectory share this one
// rendering path. Returns "" when the directory doesn't exist. Every tile
// carries data-search so the shared header filter works; a directory tile's
// key is the union of its descendants' keys, so filtering never hides a
// section that still has a match inside.
func (s *Server) docsFolderHTML(docs []docEntry, dir string) string {
	node := findDocsNode(docsTree(docs), dir)
	if node == nil {
		return ""
	}
	if dir == "" {
		// Nothing but the guide ships, and the guide has its own tree below.
		delete(node.dirs, userGuideDir)
	}
	titles := dirTitles(docs)
	var b strings.Builder
	indexSlug, prefix := "index", ""
	if dir != "" {
		indexSlug, prefix = dir+"/index", dir+"/"
	}
	for _, d := range docs {
		if d.slug == indexSlug {
			if raw, err := os.ReadFile(d.path); err == nil {
				rendered := s.themedDocImages(absDocLinks(docMasthead(markdown.Render(string(raw))), dir))
				if inUserGuide(dir) {
					rendered = renameProductHTML(stripLeadingH1(rendered))
				}
				b.WriteString(`<div class="prose">` + rendered + `</div>`)
			}
			break
		}
	}
	technique := func(slug, title, summary, search string) {
		fmt.Fprintf(&b, `<a class="doc-technique" href="/docs/%s" data-search="%s">`+
			`<span class="doc-technique-name">%s</span><span class="doc-technique-sum">%s</span></a>`,
			html.EscapeString(slug), html.EscapeString(strings.ToLower(search)),
			html.EscapeString(title), html.EscapeString(summary))
	}
	b.WriteString(`<div class="doc-techniques">`)
	dirs := make([]string, 0, len(node.dirs))
	for name := range node.dirs {
		dirs = append(dirs, name)
	}
	sort.Strings(dirs)
	for _, name := range dirs {
		child, slug := node.dirs[name], prefix+name
		label, summary := dirLabel(titles, slug, name), ""
		for _, d := range docs {
			if d.slug == slug+"/index" {
				summary = guideText(d.slug, docSummary(d.path))
				break
			}
		}
		if summary == "" {
			summary = fmt.Sprintf("%d page%s", child.count(), plural(child.count()))
		}
		technique(slug, label, summary, name+" "+label+" "+child.searchKeys())
	}
	for _, d := range node.docs {
		if isDocIndex(d.slug) {
			continue // rendered as the folder's description, not carded
		}
		technique(d.slug, d.title, guideText(d.slug, docSummary(d.path)), d.slug+" "+d.title)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// count is the number of documents under a node, at any depth, not counting
// folder index files — the "N pages" fallback for a category tile whose
// directory has no index summary.
func (n *docNode) count() int {
	c := 0
	for _, d := range n.docs {
		if !isDocIndex(d.slug) {
			c++
		}
	}
	for _, child := range n.dirs {
		c += child.count()
	}
	return c
}

var docInlineMdRe = regexp.MustCompile("\\[([^\\]]+)\\]\\([^)]*\\)|[*_`]+")

// docSummary is a page tile's blurb: the document's first paragraph of plain
// body text — headings, images, lists, tables, and fences don't summarize —
// stripped of inline markup and truncated at a word boundary.
func docSummary(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var para []string
	for _, line := range strings.Split(string(raw), "\n") {
		l := strings.TrimSpace(line)
		switch {
		case l == "":
			if len(para) > 0 {
				return clipSummary(strings.Join(para, " "))
			}
		case strings.HasPrefix(l, "#") || strings.HasPrefix(l, "!") ||
			strings.HasPrefix(l, "|") || strings.HasPrefix(l, ">") ||
			strings.HasPrefix(l, "```") || strings.Trim(l, "-*_") == "" ||
			ulItemStart(l) || olItemStart(l):
			if len(para) > 0 {
				return clipSummary(strings.Join(para, " "))
			}
		default:
			para = append(para, l)
		}
	}
	return clipSummary(strings.Join(para, " "))
}

func ulItemStart(l string) bool {
	return (strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasPrefix(l, "+ "))
}

func olItemStart(l string) bool {
	i := 0
	for i < len(l) && l[i] >= '0' && l[i] <= '9' {
		i++
	}
	return i > 0 && i < len(l) && l[i] == '.'
}

// clipSummary strips inline markdown (links keep their text) and truncates to
// a word boundary near 160 characters.
func clipSummary(s string) string {
	s = docInlineMdRe.ReplaceAllString(s, "$1")
	s = strings.TrimSpace(s)
	if len(s) <= 160 {
		return s
	}
	cut := s[:160]
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.") + "…"
}

// docHrefRe matches href and src attributes in rendered doc HTML.
var docHrefRe = regexp.MustCompile(`(href|src)="([^"]+)"`)

// absDocLinks rewrites the relative links in a rendered document to absolute
// /docs/ paths. Docs cross-link relatively ([design.md](design.md),
// ../dev/09-testing.md) — correct on GitHub and in editors — but relative
// resolution in the browser depends on whether the current URL has a
// trailing slash, and folder pages ("/docs/design") don't. Resolving
// server-side, where the source document's directory is known, makes every
// internal link independent of how the page was reached. src attributes get
// the same treatment so embedded images (user-guide screenshots) resolve too;
// only page targets have a .md suffix to trim, so one rule serves both.
func absDocLinks(rendered, baseDir string) string {
	return docHrefRe.ReplaceAllStringFunc(rendered, func(m string) string {
		attr, target, _ := strings.Cut(m, `="`)
		target = target[:len(target)-1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "/") ||
			strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") ||
			strings.HasPrefix(target, "data:") {
			return m
		}
		target, anchor, _ := strings.Cut(target, "#")
		if anchor != "" {
			anchor = "#" + anchor
		}
		resolved := path.Join("/docs", baseDir, strings.TrimSuffix(target, ".md"))
		return attr + `="` + resolved + anchor + `"`
	})
}

// docImgRe matches an embedded doc image after absDocLinks has resolved its
// src to an absolute /docs/ path.
var docImgRe = regexp.MustCompile(`<img src="/docs/([^"]+)"([^>]*)>`)

// themedDocImages pairs each embedded screenshot with its dark-scheme sibling
// — name-dark.ext beside name.ext in the docs tree — when one exists. Both
// variants are emitted and app.css displays whichever matches the viewer's
// theme (the manual data-theme toggle, falling back to the OS scheme), so a
// dark dashboard shows dark screenshots. An image with no sibling stays a
// single img serving both themes; on GitHub, which renders the markdown
// directly, only the light image is referenced.
//
// Every src carries ?v=<mtime>: a regenerated screenshot is a NEW URL, so
// every client re-fetches it immediately — including installed home-screen
// web apps, which offer no way to hard-refresh a stale cache.
func (s *Server) themedDocImages(rendered string) string {
	return docImgRe.ReplaceAllStringFunc(rendered, func(m string) string {
		sub := docImgRe.FindStringSubmatch(m)
		ext := path.Ext(sub[1])
		dark := strings.TrimSuffix(sub[1], ext) + "-dark" + ext
		if _, err := os.Stat(filepath.Join(s.DocsDir, filepath.FromSlash(dark))); err != nil {
			return `<img src="/docs/` + s.docAssetVersioned(sub[1]) + `"` + sub[2] + `>`
		}
		return `<img class="theme-light" src="/docs/` + s.docAssetVersioned(sub[1]) + `"` + sub[2] + `>` +
			`<img class="theme-dark" src="/docs/` + s.docAssetVersioned(dark) + `"` + sub[2] + `>`
	})
}

// docAssetVersioned appends the file's mtime as a cache-busting version to a
// docs-relative asset path. Unstat-able files pass through unversioned.
func (s *Server) docAssetVersioned(rel string) string {
	fi, err := os.Stat(filepath.Join(s.DocsDir, filepath.FromSlash(rel)))
	if err != nil {
		return rel
	}
	return rel + "?v=" + strconv.FormatInt(fi.ModTime().Unix(), 10)
}

// docAssetExts is the set of binary files the docs tree may embed. Markdown
// stays the only page type; this is just what a page may reference.
var docAssetExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true,
}

// docsAssetOr serves image files living beside the markdown docs (the user
// guide embeds screenshots); every other slug falls through to the HTML view.
// Assets carry the same sign-in gate as the pages that embed them, and the
// path is confined to DocsDir so ../ traversal can't escape it.
func (s *Server) docsAssetOr(pages http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if !docAssetExts[strings.ToLower(path.Ext(slug))] {
			pages(w, r)
			return
		}
		public := s.authRequired() && s.sessionUser(r) == nil && inUserGuide(strings.Trim(slug, "/"))
		if s.authRequired() && s.sessionUser(r) == nil && !public {
			http.Error(w, "sign in to view documentation", http.StatusUnauthorized)
			return
		}
		root, err := filepath.Abs(s.DocsDir)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(root, filepath.FromSlash(path.Clean("/"+slug)))
		if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		// A guide illustration is the same picture for every anonymous reader of
		// this tenant, so it is bucket A with a short browser TTL — which is also
		// what the old no-cache was reaching for. no-cache itself is now
		// forbidden on a public response: it means "store it but revalidate",
		// which is a worse version of a small max-age and cancels the stale
		// directives that keep the edge from stampeding the origin. ServeFile
		// still answers If-Modified-Since with a 304, so a regenerated screenshot
		// arrives on the next plain reload.
		if public {
			cachepolicy.MarkPublic(w, r, "user-guide asset")
		} else {
			cachepolicy.MarkPrivate(w, r, "members-only docs asset")
		}
		http.ServeFile(w, r, full)
	}
}

// userGuideDir is the docs subdirectory the guide lives in, and since the move
// of the design records out of this repository it is the whole of docs/.
const userGuideDir = "user-guide"

// inUserGuide reports whether a doc or folder slug belongs to the guide.
func inUserGuide(slug string) bool {
	return slug == userGuideDir || strings.HasPrefix(slug, userGuideDir+"/")
}

// docsRequestPublic reports whether a /docs/{slug...} request targets the user
// guide — the one docs section served without a session. The slug arrives with
// the same shapes pageDocs accepts (a trailing .md, surrounding slashes), so it
// is normalized the same way before the guide test.
func docsRequestPublic(r *http.Request) bool {
	slug := strings.TrimSuffix(r.PathValue("slug"), ".md")
	return inUserGuide(strings.Trim(slug, "/"))
}

// docExampleRegistry is the stand-in address the user guide writes wherever it
// shows a member a command aimed at "your registry". The source has to name
// something — GitHub and editors render the markdown as written, and no
// hostname is true for every reader — but a member reading the guide IN the
// dashboard is already talking to the registry the commands should point at.
const docExampleRegistry = "https://tacit.example.com"

// guideRegistryURL rewrites the guide's stand-in registry address to this
// registry's real one, so a command copied out of the dashboard's guide is
// already pointed at the registry that served it. The address is the same one
// invites hand out (externalBaseFor): the published or configured external
// URL, else the host this request arrived on — so a member browsing through a
// tunnel gets the tunnel address, not an internal one.
//
// Only user-guide pages are rewritten. Any other tree an operator points
// --docs at uses the same hostname
// to mean some example deployment rather than this one, and rewriting it there
// would put a live address into a design document's prose.
func guideRegistryURL(rendered, slug, base string) string {
	if base == "" || !inUserGuide(slug) {
		return rendered
	}
	return strings.ReplaceAll(rendered, docExampleRegistry, html.EscapeString(base))
}

var leadingH1Re = regexp.MustCompile(`(?s)\A\s*<h1>.*?</h1>\s*`)

// stripLeadingH1 drops a rendered document's opening <h1>. User-guide pages
// already show their title as the current breadcrumb, so repeating it in the
// content is noise; the heading stays in the markdown source, where GitHub
// and editors still need it.
func stripLeadingH1(doc string) string {
	return leadingH1Re.ReplaceAllString(doc, "")
}

// dirTitles maps each directory slug to its index.md's title — the label the
// contents menu, folder tree, and breadcrumbs show for that folder. A numeric
// prefix on the directory name (10-get-started) orders the tree without
// showing: the index title is the visible name.
func dirTitles(docs []docEntry) map[string]string {
	titles := map[string]string{}
	for _, d := range docs {
		if isDocIndex(d.slug) && d.slug != "index" {
			titles[path.Dir(d.slug)] = d.title
		}
	}
	return titles
}

// dirLabel is a folder's display name: its index.md title when it has one,
// else the raw directory name.
func dirLabel(titles map[string]string, slug, name string) string {
	if t := titles[slug]; t != "" {
		return t
	}
	return name
}

// docCrumbs builds the trail for a doc or folder page: every ancestor
// directory links to its folder page, so the breadcrumb is also the way back
// up the hierarchy. Every page trails back to Documentation, which is the
// guide. Directory crumbs show their index title.
func docCrumbs(slug, leaf string, titles map[string]string) []crumb {
	root, acc := crumb{label: "Documentation", href: "/docs"}, ""
	if slug == userGuideDir {
		return []crumb{{label: "Documentation", href: ""}}
	}
	if inUserGuide(slug) {
		root, acc = crumb{label: "Documentation", href: "/docs/" + userGuideDir}, userGuideDir
		slug = strings.TrimPrefix(slug, userGuideDir+"/")
	}
	crumbs := []crumb{root}
	if dir := path.Dir(slug); dir != "." {
		for _, part := range strings.Split(dir, "/") {
			acc = path.Join(acc, part)
			crumbs = append(crumbs, crumb{label: dirLabel(titles, acc, part), href: "/docs/" + acc})
		}
	}
	return append(crumbs, crumb{label: leaf, href: ""})
}

// docEntry's slug is the path relative to DocsDir without the .md suffix —
// "architecture" for a top-level doc, "dev/03-registry" for a nested one.
type docEntry struct{ slug, title, path string }

func (s *Server) listDocs() []docEntry {
	var docs []docEntry
	_ = filepath.WalkDir(s.DocsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, err := filepath.Rel(s.DocsDir, p)
		if err != nil {
			return nil
		}
		slug := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
		// The one place a guide title is read: everything visible that names a
		// document — the contents rail and its filter key, the breadcrumbs, the
		// pager, the folder listings — is built from here, so renaming it once
		// keeps all of them saying the same thing. The slug is untouched: it is
		// the address, and the guide's own cross-links are written against it.
		docs = append(docs, docEntry{slug, guideText(slug, docTitle(p, path.Base(slug))), p})
		return nil
	})
	sort.Slice(docs, func(i, j int) bool { return docs[i].slug < docs[j].slug })
	return docs
}

// docTitle returns the doc's own `# Title` line if present, else a
// title-cased slug.
func docTitle(path, slug string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return slug
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	first = strings.TrimSpace(first)
	if strings.HasPrefix(first, "# ") {
		return strings.TrimSpace(first[2:])
	}
	return titleCase(strings.ReplaceAll(slug, "-", " "))
}

// titleCase upper-cases the first letter of each ASCII word (doc slugs only).
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func (s *Server) pageDocs(r *http.Request, user oidc.Claims) page {
	slug := r.PathValue("slug")
	// docs cross-link each other as relative [name.md](name.md) links (valid on
	// GitHub and in editors); accept the .md-suffixed form here too.
	slug = strings.TrimSuffix(slug, ".md")
	slug = strings.Trim(slug, "/")
	// A folder's index.md IS the folder page's opening content — a link to it
	// (docs cross-link [section](dir/index.md)) must land on the folder page
	// with its techniques, not on a bare document that would double the trail's
	// last crumb.
	if isDocIndex(slug) {
		slug = strings.Trim(strings.TrimSuffix(slug, "index"), "/")
	}
	docs := s.listDocs()
	if slug == "" {
		// /docs is the guide. Nothing else ships, so a root listing would be a
		// page whose only entry is the page you wanted. An operator pointing
		// --docs at a tree of their own may have no guide in it; that tree
		// still gets its listing.
		if findDocsNode(docsTree(docs), userGuideDir) != nil {
			slug = userGuideDir
		} else {
			return page{active: "docs",
				crumbs:  docsSectionCrumbs(),
				content: docsLayout(docsSideNav(docs, ""), s.docsFolderHTML(docs, ""), "")}
		}
	}
	for _, d := range docs {
		if d.slug == slug {
			raw, err := os.ReadFile(d.path)
			if err != nil {
				break
			}
			baseDir := path.Dir(slug)
			if baseDir == "." {
				baseDir = ""
			}
			rendered := s.themedDocImages(absDocLinks(docMasthead(markdown.Render(string(raw))), baseDir))
			rendered = guideRegistryURL(rendered, slug, s.externalBaseFor(r))
			if inUserGuide(slug) {
				rendered = stripLeadingH1(rendered)
			}
			rendered, toc := docTOC(rendered)
			if inUserGuide(slug) {
				// After docTOC, so the ids it derived from the authored headings
				// still match the links written against them.
				rendered, toc = renameProductHTML(rendered), renameProductHTML(toc)
			}
			return page{active: "docs", activeDoc: slug,
				crumbs: docCrumbs(slug, d.title, dirTitles(docs)),
				content: docsLayout(docsSideNav(docs, slug),
					`<div class="prose">`+rendered+`</div>`+guidePager(docs, slug), toc)}
		}
	}
	// Not a document — a folder? Its page is the index.md plus the subtree.
	if content := s.docsFolderHTML(docs, slug); content != "" {
		content = guideRegistryURL(content, slug, s.externalBaseFor(r))
		titles := dirTitles(docs)
		// The section landing carries the bare "Documentation" crumb; deeper in,
		// the trail names the document itself.
		if slug == userGuideDir {
			return page{active: "docs",
				crumbs:  docsSectionCrumbs(),
				content: docsLayout(docsSideNav(docs, slug), content, "")}
		}
		return page{active: "docs",
			crumbs:  docCrumbs(slug, dirLabel(titles, slug, path.Base(slug)), titles),
			content: docsLayout(docsSideNav(docs, slug), content, "")}
	}
	return page{status: 404, active: "docs",
		content: `<div class="doc-placeholder">Document not found. <a href="/docs">All docs.</a></div>`}
}

// docsLayout arranges a docs page docusaurus-style: the section's contents
// menu on the left, the article in the middle, and — when the page has one —
// the on-page index on the right. The has-toc class widens the grid to three
// columns; narrow viewports drop the outer panes (CSS).
func docsLayout(side, main, toc string) string {
	cls := "docs-layout"
	if toc != "" {
		cls += " has-toc"
	}
	return `<div class="` + cls + `">` + side +
		`<article class="docs-main">` + main + `</article>` + toc + `</div>`
}

// docsSideNav renders the left-hand contents menu: the guide's own tree. The
// root entry heads the menu and links to the overview; directories are
// collapsible and open along the path to the current page. Rows carry
// data-search so the header filter narrows the menu.
func docsSideNav(docs []docEntry, current string) string {
	root := docsTree(docs)
	label, rootSlug, node := "Documentation", "", root
	if inUserGuide(current) {
		// The page heading above the rail already says "User Guide"; the rail's
		// root entry names what the link IS — the guide's overview page.
		label, rootSlug, node = "Overview", userGuideDir, findDocsNode(root, userGuideDir)
	} else {
		delete(root.dirs, userGuideDir)
	}
	if node == nil {
		return ""
	}
	href, prefix := "/docs", ""
	if rootSlug != "" {
		href, prefix = "/docs/"+rootSlug, rootSlug+"/"
	}
	cls := "side-root"
	if current == rootSlug {
		cls += " active"
	}
	var b strings.Builder
	b.WriteString(`<nav class="docs-side" aria-label="Documentation contents"><div class="rail-scroll">`)
	fmt.Fprintf(&b, `<a class="%s" href="%s">%s</a>`, cls, href, label)
	renderSideNode(&b, node, prefix, current, dirTitles(docs))
	b.WriteString(`</div></nav>`)
	return b.String()
}

func renderSideNode(b *strings.Builder, n *docNode, prefix, current string, titles map[string]string) {
	dirs := make([]string, 0, len(n.dirs))
	for name := range n.dirs {
		dirs = append(dirs, name)
	}
	sort.Strings(dirs)
	b.WriteString(`<ul>`)
	for _, name := range dirs {
		child, slug := n.dirs[name], prefix+name
		label := dirLabel(titles, slug, name)
		open, cls := "", ""
		if current == slug || strings.HasPrefix(current, slug+"/") {
			open = " open"
		}
		if current == slug {
			cls = ` class="active"`
		}
		fmt.Fprintf(b, `<li class="side-dir" data-search="%s"><details%s><summary><a%s href="/docs/%s">%s</a></summary>`,
			html.EscapeString(strings.ToLower(name+" "+label+" "+child.searchKeys())),
			open, cls, html.EscapeString(slug), html.EscapeString(label))
		renderSideNode(b, child, slug+"/", current, titles)
		b.WriteString(`</details></li>`)
	}
	for _, d := range n.docs {
		if isDocIndex(d.slug) {
			continue // the folder link above already leads to it
		}
		cls := "side-link"
		if d.slug == current {
			cls += " active"
		}
		fmt.Fprintf(b, `<li data-search="%s"><a class="%s" href="/docs/%s">%s</a></li>`,
			html.EscapeString(strings.ToLower(d.slug+" "+d.title)),
			cls, html.EscapeString(d.slug), html.EscapeString(d.title))
	}
	b.WriteString(`</ul>`)
}

// guidePager renders the Previous/Next footer on user-guide chapter pages.
// The reading order is slug order — the numbered groups and chapters — so
// Next walks the whole guide, crossing group boundaries. Chapters only:
// non-guide docs and folder indexes aren't part of the sequence.
func guidePager(docs []docEntry, slug string) string {
	if !inUserGuide(slug) {
		return ""
	}
	var seq []docEntry
	for _, d := range docs {
		if inUserGuide(d.slug) && !isDocIndex(d.slug) {
			seq = append(seq, d) // docs are already slug-sorted
		}
	}
	for i, d := range seq {
		if d.slug != slug {
			continue
		}
		if len(seq) == 1 {
			return ""
		}
		var b strings.Builder
		b.WriteString(`<nav class="doc-pager" aria-label="Guide pages">`)
		if i > 0 {
			fmt.Fprintf(&b, `<a class="pager-prev" href="/docs/%s"><span class="pager-dir">Previous</span>`+
				`<span class="pager-title">« %s</span></a>`,
				html.EscapeString(seq[i-1].slug), html.EscapeString(seq[i-1].title))
		}
		if i < len(seq)-1 {
			fmt.Fprintf(&b, `<a class="pager-next" href="/docs/%s"><span class="pager-dir">Next</span>`+
				`<span class="pager-title">%s »</span></a>`,
				html.EscapeString(seq[i+1].slug), html.EscapeString(seq[i+1].title))
		}
		b.WriteString(`</nav>`)
		return b.String()
	}
	return ""
}

// docHeadingRe matches the rendered h2/h3 headings a document's on-page index
// is built from (h1 is the title; deeper levels are noise in a sidebar).
var docHeadingRe = regexp.MustCompile(`(?s)<h([23])>(.*?)</h[23]>`)

var docTagRe = regexp.MustCompile(`<[^>]*>`)

// docTOC gives each h2/h3 in a rendered document an anchor id and returns the
// rewritten document plus the right-hand "On this page" nav. Documents with
// fewer than two sections return an empty nav — an index of one entry is
// chrome without information.
func docTOC(doc string) (string, string) {
	type tocItem struct {
		level    int
		id, text string
	}
	var items []tocItem
	used := map[string]bool{}
	doc = docHeadingRe.ReplaceAllStringFunc(doc, func(m string) string {
		sub := docHeadingRe.FindStringSubmatch(m)
		text := strings.TrimSpace(docTagRe.ReplaceAllString(sub[2], ""))
		id := headingID(text)
		for n := 1; used[id]; n++ {
			id = fmt.Sprintf("%s-%d", headingID(text), n)
		}
		used[id] = true
		items = append(items, tocItem{int(sub[1][0] - '0'), id, text})
		return fmt.Sprintf(`<h%s id="%s">%s</h%s>`, sub[1], id, sub[2], sub[1])
	})
	if len(items) < 2 {
		return doc, ""
	}
	var t strings.Builder
	t.WriteString(`<nav class="docs-toc" aria-label="On this page"><div class="rail-scroll"><div class="toc-title">On this page</div><ul>`)
	for _, it := range items {
		fmt.Fprintf(&t, `<li class="toc-l%d"><a href="#%s">%s</a></li>`, it.level, it.id, it.text)
	}
	t.WriteString(`</ul></div></nav>`)
	return doc, t.String()
}

// headingID slugifies a heading's text into an anchor id, GitHub-style:
// lower-cased, runs of non-alphanumerics collapsed to single hyphens.
func headingID(text string) string {
	var out []rune
	for _, r := range strings.ToLower(html.UnescapeString(text)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out = append(out, r)
		case len(out) > 0 && out[len(out)-1] != '-':
			out = append(out, '-')
		}
	}
	id := strings.Trim(string(out), "-")
	if id == "" {
		return "section"
	}
	return id
}
