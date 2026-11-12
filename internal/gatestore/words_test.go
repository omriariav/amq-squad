package gatestore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// TestBareWordsAreTheDecidedList fixes D3's words, order and normalization.
func TestBareWordsAreTheDecidedList(t *testing.T) {
	wantYes := []string{"yes", "approve", "y", "ok", "go ahead", "\u05db\u05df"}
	wantNo := []string{"no", "deny", "n", "stop", "\u05dc\u05d0"}
	if !reflect.DeepEqual(yesWords, wantYes) || !reflect.DeepEqual(noWords, wantNo) {
		t.Fatalf("bare words: yes %#v no %#v, want yes %#v no %#v", yesWords, noWords, wantYes, wantNo)
	}
	for _, word := range append(append([]string{}, yesWords...), noWords...) {
		if strings.TrimSpace(word) != word || strings.ToLower(word) != word || strings.IndexFunc(word, unicode.IsPunct) >= 0 {
			t.Errorf("word %q is not lower-case, trimmed and punctuation-free", word)
		}
	}
}

// TestParseBareBindsEachWordAloneInAnyCaseWithOneMark exercises every
// decided word across the accepted case, spacing and one-mark forms.
func TestParseBareBindsEachWordAloneInAnyCaseWithOneMark(t *testing.T) {
	for _, tc := range decidedWordCases() {
		w, verb := tc.word, tc.verb
		forms := []string{
			w, strings.ToUpper(w), titleBareWord(w), " " + w, w + " ", "\t" + w + "\t",
			w + "\n", "\r\n" + w + "\r\n", "\u00a0" + w + "\u3000",
			strings.ToUpper(w) + "!", titleBareWord(w) + ".", " " + w + ".\n",
			w + " .", w + " ?", " " + strings.ToUpper(w) + " ! ",
		}
		for _, mark := range []string{".", "!", "?", ",", ";", ":", "\u2026", "\uff01", "\u05c3"} {
			forms = append(forms, w+mark)
		}
		for _, in := range forms {
			if got, ok := ParseBare(in); !ok || got != verb {
				t.Errorf("ParseBare(%q) = %q %v, want %q", in, got, ok, verb)
			}
		}
	}
	for _, in := range []string{"ok?", "o\u212a", "O\u212a"} {
		if got, ok := ParseBare(in); !ok || got != "approve" {
			t.Errorf("ParseBare(%q) = %q %v, want approve", in, got, ok)
		}
	}
}

// TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine exercises the
// two-mark boundary and every decided exclusion around a word.
func TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine(t *testing.T) {
	for _, tc := range decidedWordCases() {
		w := tc.word
		forms := []string{
			w + "!!", w + "??", w + "..", w + "...", w + "?!", w + ".!", w + "! !", w + ". .", w + " !!", w + ").", w + "!\u2026",
			w + ", but", w + ", but\u2026", w + " but wait for CI", w + "? and then", w + " please", "please " + w, w + " " + w,
			w + "\nno", w + "\nyes", w + "\n\n> the question", w + " rel-1", w + " 0123456789abcdef",
			"." + w, "\u00bf" + w, "(" + w + ")", "\"" + w + "\"", "*" + w + "*", "> " + w, ">" + w, "- " + w,
			w + "+", w + "$", w + "=", w + "\U0001f44d", w + " \U0001f44d",
			"\u200b" + w, w + "\u200d", w + "\u200f", "\u200f" + w, "\u200e" + w, "\ufeff" + w, w + "\x00",
		}
		for _, in := range forms {
			if got, ok := ParseBare(in); ok {
				t.Errorf("ParseBare(%q) = %q, want no binding", in, got)
			}
		}
	}
	for _, in := range []string{
		"", " ", "\n", ".", "?", "ok? and then", "yes but wait for CI", "sure", "okay", "k", "yep", "yeah", "nope", "nah", "approved", "denied", "stop it", "y/n", "yes/no",
		"go", "ahead", "goahead", "go-ahead", "go  ahead", "go\tahead", "go\u00a0ahead", "go, ahead", "\uff59\uff45\uff53",
		"\u05db\u05b5\u05bc\u05df", "\u05dc\u05b9\u05d0", "\u05db\u05e0", "\u05db\u05df \u05db\u05df",
		"approve rel-1 release 0123456789abcdef", "\U0001f44d",
	} {
		if got, ok := ParseBare(in); ok {
			t.Errorf("ParseBare(%q) = %q, want no binding", in, got)
		}
	}
}

// TestNoOtherFileSpellsAYesNoList parses production Go and permits only the
// three worktree --yes flags. It cannot see lists made only from the other
// six Latin words, regular expressions, non-Go files or generated strings.
func TestNoOtherFileSpellsAYesNoList(t *testing.T) {
	targets := map[string]bool{"yes": true, "no": true, "go ahead": true, "\u05db\u05df": true, "\u05dc\u05d0": true}
	got := map[string]map[string]int{}
	files := 0
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join("..", "..", top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(filepath.Join("..", ".."), path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "internal/gatestore/words.go" {
				return nil
			}
			files++
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(parsed, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err == nil {
					word := strings.ToLower(strings.TrimSpace(value))
					if targets[word] {
						if got[rel] == nil {
							got[rel] = map[string]int{}
						}
						got[rel][word]++
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 100 {
		t.Fatalf("guard parsed only %d production Go files", files)
	}
	want := map[string]map[string]int{"internal/cli/worktree.go": {"yes": 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bare-answer spellings outside words.go: got %#v, want %#v", got, want)
	}
}

type decidedWordCase struct {
	word string
	verb string
}

// decidedWordCases spells the public contract without reading the package lists.
func decidedWordCases() []decidedWordCase {
	return []decidedWordCase{
		{"yes", "approve"}, {"approve", "approve"}, {"y", "approve"}, {"ok", "approve"}, {"go ahead", "approve"}, {"\u05db\u05df", "approve"},
		{"no", "deny"}, {"deny", "deny"}, {"n", "deny"}, {"stop", "deny"}, {"\u05dc\u05d0", "deny"},
	}
}

// titleBareWord upper-cases the first rune of each U+0020-separated part.
func titleBareWord(word string) string {
	parts := strings.Split(word, " ")
	for i, part := range parts {
		r, size := utf8.DecodeRuneInString(part)
		parts[i] = string(unicode.ToUpper(r)) + part[size:]
	}
	return strings.Join(parts, " ")
}
