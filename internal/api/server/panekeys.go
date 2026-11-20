package server

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/api"
)

// paneModifiers are the prefixes tmux reads for each modifier a chord may hold, in the order the
// server writes them (https://man.openbsd.org/tmux.1#KEY_BINDINGS): C- for Ctrl, M- (Meta) for Alt,
// and S- for Shift.
var paneModifiers = []struct{ name, prefix string }{{"Ctrl", "C-"}, {"Alt", "M-"}, {"Shift", "S-"}}

// paneKeyNames reads a named key in any case, as tmux names it.
var paneKeyNames = func() map[string]string {
	names := make(map[string]string, len(api.PaneKeyNames))
	for _, name := range api.PaneKeyNames {
		names[strings.ToLower(name)] = name
	}
	return names
}()

// paneSequence reads a key sequence in the pane's key grammar (api.PaneInput) into tmux's name for
// each chord, which the terminal sends with key name lookup. Anything outside the grammar is refused,
// so no string a page sends reaches tmux as anything but a key it names.
func paneSequence(sequence string) ([]string, error) {
	switch {
	case sequence == "":
		return nil, errors.New("no keys to send")
	case len(sequence) > api.MaxPaneKeys:
		return nil, fmt.Errorf("the keys are longer than %d bytes", api.MaxPaneKeys)
	case !utf8.ValidString(sequence):
		return nil, errors.New("the keys are not UTF-8")
	case strings.ContainsFunc(sequence, func(r rune) bool { return r != ' ' && !unicode.IsPrint(r) }):
		return nil, errors.New("the keys hold a control character: name the key instead, such as Escape or Ctrl-C")
	}
	chords := strings.Split(sequence, " ")
	if len(chords) > api.MaxPaneChords {
		return nil, fmt.Errorf("a sequence holds at most %d chords, and this one holds %d", api.MaxPaneChords, len(chords))
	}
	keys := make([]string, 0, len(chords))
	for _, chord := range chords {
		key, err := paneChord(chord)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// paneChord reads one chord: its modifiers, each once, then a named key or one character.
func paneChord(chord string) (string, error) {
	if chord == "" {
		return "", errors.New("chords are separated by one space, with none before the first or after the last")
	}
	held := map[string]bool{}
	rest := chord
	for {
		name, after, ok := paneModifier(rest)
		if !ok {
			break
		}
		if held[name] {
			return "", fmt.Errorf("%q holds %s twice", chord, name)
		}
		held[name] = true
		rest = after
	}
	key, err := paneKey(chord, rest, held)
	if err != nil {
		return "", err
	}
	var prefix strings.Builder
	for _, modifier := range paneModifiers {
		if held[modifier.name] {
			prefix.WriteString(modifier.prefix)
		}
	}
	return prefix.String() + key, nil
}

// paneModifier reads a modifier and its hyphen from the start of a chord, when a key follows them.
func paneModifier(chord string) (string, string, bool) {
	for _, modifier := range paneModifiers {
		head := modifier.name + "-"
		if len(chord) > len(head) && strings.EqualFold(chord[:len(head)], head) {
			return modifier.name, chord[len(head):], true
		}
	}
	return "", "", false
}

// paneKey names the key a chord ends with. Shift-Tab is BTab, tmux's name for it, because tmux 3.8 sends
// S-Tab as a plain Tab. Shift goes only with a named key, because a character carries its own, and tmux
// 3.8 drops the Shift of S-a. A letter with Ctrl is sent lowercase, because a terminal sends Ctrl-X and
// Ctrl-x as one byte.
func paneKey(chord, key string, held map[string]bool) (string, error) {
	if name, ok := paneKeyNames[strings.ToLower(key)]; ok {
		if name == "Tab" && held["Shift"] {
			delete(held, "Shift")
			return "BTab", nil
		}
		return name, nil
	}
	if utf8.RuneCountInString(key) != 1 {
		return "", fmt.Errorf("%q names no key: a chord ends with one character or one of %s, after any of %s",
			chord, strings.Join(api.PaneKeyNames, ", "), strings.Join(api.PaneModifiers, ", "))
	}
	if key == " " {
		return "", fmt.Errorf("%q ends with a space: name it Space", chord)
	}
	if held["Shift"] {
		return "", fmt.Errorf("%q puts Shift on a character: send the character as Shift makes it, such as A", chord)
	}
	if held["Ctrl"] && key >= "A" && key <= "Z" {
		return strings.ToLower(key), nil
	}
	return key, nil
}
