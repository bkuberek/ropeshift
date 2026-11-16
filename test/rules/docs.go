package rules

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// MaxImageBytes is the most a published image weighs. A page of the user docs shows several, so a larger
// one slows every reader of the repository and the website.
const MaxImageBytes = 250 * 1000

// singleImages are the images a doc may show without a light and a dark file: the wordmark, and the
// terminal view, which is a terminal's own dark screen in either theme.
var singleImages = []string{"docs/assets/ropeshift-banner.png", "docs/assets/screenshots/top.png"}

const (
	lightFragment = "gh-light-mode-only"
	darkFragment  = "gh-dark-mode-only"
	screenshots   = "docs/assets/screenshots"
)

var (
	// imageName is a published image's file name: lowercase words joined by hyphens, and a .png extension.
	imageName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*\.png$`)
	// repoBrowse is a link to the product repository's files on the forge, which a relative link replaces.
	repoBrowse = regexp.MustCompile(`github\.com/[^/\s)]+/ropeshift/(blob|tree)/`)
	openSource = regexp.MustCompile(`(?i)open[- ]source`)
	heading    = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	inlineLink = regexp.MustCompile(`\]\(([^)\s]*)(?:\s+"[^"]*")?\)`)
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)

// docLink is one inline link or image of a doc.
type docLink struct {
	line   int
	target string
	image  bool
}

// UserDocs lists the docs written for people who use ropeshift: the README and every Markdown file
// directly in docs/. The maintainer docs under docs/development are not among them.
func UserDocs(root string) ([]string, error) {
	files := []string{"README.md"}
	entries, err := os.ReadDir(filepath.Join(root, "docs"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			files = append(files, "docs/"+entry.Name())
		}
	}
	return files, nil
}

// CheckUserDocs holds the user docs to their images and links, and answers each violation as
// "file:line: what is wrong". Every image a doc shows exists, is a lowercase .png under docs/assets, comes
// as a light file and then its dark file, and weighs at most MaxImageBytes. Every file of
// docs/assets/screenshots is shown, and listed in the table of docs/development/screenshots.md. Every relative link and anchor resolves, no link names the product
// repository's files on the forge, and no doc calls the product open source.
func CheckUserDocs(root string) ([]string, error) {
	files, err := UserDocs(root)
	if err != nil {
		return nil, err
	}
	var found []string
	shown := map[string]bool{}
	anchors := map[string][]string{}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		found = append(found, checkWords(file, string(raw))...)
		prose := proseOf(string(raw))
		found = append(found, checkLinks(root, file, prose, anchors)...)
		images, problems := checkImages(root, file, prose)
		found = append(found, problems...)
		for _, image := range images {
			shown[image] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(screenshots)))
	if err != nil {
		return nil, err
	}
	listed, problem := publishedNames(root)
	if problem != "" {
		found = append(found, problem)
	}
	for _, entry := range entries {
		name := screenshots + "/" + entry.Name()
		if !shown[name] {
			found = append(found, name+": no user doc shows this image")
		}
		if problem == "" && !slices.Contains(listed, entry.Name()) {
			found = append(found, name+": "+publishedTable+" has no row that publishes this image")
		}
	}
	for _, name := range listed {
		if !slices.ContainsFunc(entries, func(entry os.DirEntry) bool { return entry.Name() == name }) {
			found = append(found, fmt.Sprintf("%s: a row publishes %s, which is not in %s", publishedTable, name, screenshots))
		}
	}
	slices.Sort(found)
	return found, nil
}

// publishedTable is the maintainer doc whose table lists every published image and the capture it comes from.
const publishedTable = "docs/development/screenshots.md"

// publishedRow is a row of that table: the capture and the published name, each in backticks.
var publishedRow = regexp.MustCompile("^\\| `[^`]+` \\| `([^`]+)` \\|")

// publishedNames is the file name of every image the table publishes, with <theme> as light and as dark, or
// what is wrong with the table.
func publishedNames(root string) ([]string, string) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(publishedTable)))
	if err != nil {
		return nil, publishedTable + ": " + err.Error()
	}
	var names []string
	for line := range strings.Lines(string(raw)) {
		match := publishedRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if strings.Contains(match[1], "<theme>") {
			names = append(names, strings.ReplaceAll(match[1], "<theme>", "light"), strings.ReplaceAll(match[1], "<theme>", "dark"))
		} else {
			names = append(names, match[1])
		}
	}
	return names, ""
}

// checkWords finds what no user doc says: a link to the product repository's files on the forge, and the
// product called open source, which its license does not make it.
func checkWords(file, text string) []string {
	var found []string
	for number, line := range strings.Split(text, "\n") {
		if repoBrowse.MatchString(line) {
			found = append(found, fmt.Sprintf("%s:%d: link the file by a relative path, not on the forge", file, number+1))
		}
		if openSource.MatchString(line) {
			found = append(found, fmt.Sprintf("%s:%d: the product is fair source, never open source", file, number+1))
		}
	}
	return found
}

// proseOf blanks the fenced code blocks and the inline code of a doc, keeping every line where it was, so a
// link shown as an example is not read as a link.
func proseOf(text string) string { return blankCode(text, true) }

// blankCode blanks the fenced code blocks of a doc, and the inline code too when inline is set.
func blankCode(text string, inline bool) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			lines[i] = ""
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			fence = trimmed[:3]
			lines[i] = ""
		case inline:
			lines[i] = inlineCode.ReplaceAllString(line, "")
		}
	}
	return strings.Join(lines, "\n")
}

// linksOf finds every inline link and image of the prose. A badge is an image inside a link, so each
// closing bracket and parenthesis pair is matched back to its opening bracket.
func linksOf(prose string) []docLink {
	var links []docLink
	for number, line := range strings.Split(prose, "\n") {
		for _, match := range inlineLink.FindAllStringSubmatchIndex(line, -1) {
			open := openingBracket(line, match[0])
			if open < 0 {
				continue
			}
			links = append(links, docLink{line: number + 1, target: line[match[2]:match[3]], image: open > 0 && line[open-1] == '!'})
		}
	}
	return links
}

// openingBracket is the position of the bracket that the closing one at position close matches, or -1.
func openingBracket(line string, close int) int {
	depth := 0
	for i := close; i >= 0; i-- {
		switch line[i] {
		case ']':
			depth++
		case '[':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// external is a target that leaves the repository.
func external(target string) bool {
	return strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:")
}

// resolve is the repository path a relative target of a doc names, and whether it stays inside the
// repository. The target's fragment and query are cut off and answered separately.
func resolve(file, target string) (string, string, bool) {
	target, fragment, _ := strings.Cut(target, "#")
	target, _, _ = strings.Cut(target, "?")
	if target == "" {
		return file, fragment, true
	}
	joined := path.Clean(path.Join(path.Dir(file), target))
	return joined, fragment, !strings.HasPrefix(joined, "..")
}

func checkLinks(root, file, prose string, anchors map[string][]string) []string {
	var found []string
	for _, link := range linksOf(prose) {
		if link.image || link.target == "" || external(link.target) {
			continue
		}
		where := fmt.Sprintf("%s:%d", file, link.line)
		if strings.HasPrefix(link.target, "/") {
			found = append(found, fmt.Sprintf("%s: %s is not relative", where, link.target))
			continue
		}
		target, fragment, inside := resolve(file, link.target)
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(target)))
		if !inside || err != nil {
			found = append(found, fmt.Sprintf("%s: %s does not resolve", where, link.target))
			continue
		}
		if fragment == "" || info.IsDir() || !strings.HasSuffix(target, ".md") {
			continue
		}
		if _, ok := anchors[target]; !ok {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
			if err != nil {
				found = append(found, fmt.Sprintf("%s: %s is unreadable: %v", where, link.target, err))
				continue
			}
			anchors[target] = anchorsOf(string(raw))
		}
		if !slices.Contains(anchors[target], strings.ToLower(fragment)) {
			found = append(found, fmt.Sprintf("%s: %s has no heading #%s", where, target, fragment))
		}
	}
	return found
}

// anchorsOf is the anchor of every heading of a Markdown file as GitHub makes it: the text lowercased,
// punctuation dropped, spaces turned to hyphens, and a repeated heading numbered from -1.
func anchorsOf(text string) []string {
	var anchors []string
	seen := map[string]int{}
	for line := range strings.Lines(blankCode(text, false)) {
		match := heading.FindStringSubmatch(strings.TrimRight(line, "\n"))
		if match == nil {
			continue
		}
		anchor := slug(match[1])
		if count := seen[anchor]; count > 0 {
			anchors = append(anchors, fmt.Sprintf("%s-%d", anchor, count))
		} else {
			anchors = append(anchors, anchor)
		}
		seen[anchor]++
	}
	return anchors
}

func slug(text string) string {
	text = mdLink.ReplaceAllString(text, "$1")
	var out strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			out.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			out.WriteRune(r)
		}
	}
	return out.String()
}

// checkImages holds one doc's images to the image rules, and answers the files the doc shows.
func checkImages(root, file, prose string) ([]string, []string) {
	var shown, found []string
	// light is the light file of a pair that still waits for its dark file, with the line that shows it.
	light, lightLine := "", 0
	flush := func() {
		if light != "" {
			found = append(found, fmt.Sprintf("%s:%d: %s has no dark file after it", file, lightLine, light))
		}
		light = ""
	}
	for _, link := range linksOf(prose) {
		if !link.image || link.target == "" || external(link.target) {
			continue
		}
		where := fmt.Sprintf("%s:%d", file, link.line)
		image, fragment, inside := resolve(file, link.target)
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(image)))
		if !inside || err != nil || info.IsDir() {
			flush()
			found = append(found, fmt.Sprintf("%s: the image %s does not exist", where, image))
			continue
		}
		shown = append(shown, image)
		if !strings.HasPrefix(image, "docs/assets/") || !imageName.MatchString(path.Base(image)) {
			found = append(found, fmt.Sprintf("%s: %s is not a lowercase hyphenated .png under docs/assets", where, image))
		}
		if info.Size() > MaxImageBytes {
			found = append(found, fmt.Sprintf("%s: %s is %d bytes, over the %d limit", where, image, info.Size(), MaxImageBytes))
		}
		if slices.Contains(singleImages, image) {
			flush()
			if fragment != "" {
				found = append(found, fmt.Sprintf("%s: %s has a single file and takes no #%s", where, image, fragment))
			}
			continue
		}
		switch {
		case strings.HasSuffix(image, "-light.png") && fragment == lightFragment:
			flush()
			light, lightLine = image, link.line
		case strings.HasSuffix(image, "-dark.png") && fragment == darkFragment:
			if light == "" || strings.TrimSuffix(light, "-light.png") != strings.TrimSuffix(image, "-dark.png") {
				found = append(found, fmt.Sprintf("%s: %s has no light file before it", where, image))
			}
			light = ""
		default:
			flush()
			found = append(found, fmt.Sprintf("%s: %s is a light file with #%s or a dark file with #%s", where, image, lightFragment, darkFragment))
		}
	}
	flush()
	return shown, found
}
