package status

import (
	"reflect"
	"strings"
)

// Plain replaces every C0 and C1 control character in every string the
// document carries with a space. A gate's question keeps its line breaks.
func Plain(s *Status) {
	questions := make([]string, len(s.Gates))
	for i := range s.Gates {
		questions[i] = s.Gates[i].Question
	}
	plainValue(reflect.ValueOf(s).Elem())
	for i, question := range questions {
		s.Gates[i].Question = PlainText(question, true)
	}
}

// PlainText replaces C0 and C1 controls in one terminal-bound string. When
// keepNewlines is true, U+000A is preserved while every other control is
// replaced.
func PlainText(s string, keepNewlines bool) string {
	return strings.Map(func(r rune) rune {
		if keepNewlines && r == '\n' {
			return r
		}
		if r < 0x20 || r >= 0x7f && r <= 0x9f {
			return ' '
		}
		return r
	}, s)
}

func plainValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(PlainText(v.String(), false))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			plainValue(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				plainValue(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			plainValue(v.Index(i))
		}
	}
}
