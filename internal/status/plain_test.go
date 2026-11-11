package status

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPlainCoversEveryStatusStringAndKeepsQuestionLines(t *testing.T) {
	var doc Status
	fillStatusStrings(reflect.ValueOf(&doc).Elem(), map[reflect.Type]bool{})
	doc.Gates = []Gate{{Question: "one\x1b[31m\ntwo"}}
	Plain(&doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, `\u001b`) || strings.Contains(text, `\u0007`) || !strings.Contains(text, "]0;x") {
		t.Fatalf("plain document = %s", text)
	}
	if strings.IndexFunc(text, func(r rune) bool { return r >= 0x80 && r <= 0x9f }) >= 0 {
		t.Fatalf("C1 survived: %q", text)
	}
	if got := PlainText(string([]byte{'a', 0x9b, 'b'}), false); strings.ContainsRune(got, rune(0x9b)) || strings.ContainsRune(got, '\ufffd') == false {
		t.Fatalf("invalid UTF-8 = %q", got)
	}
	if got := doc.Gates[0].Question; got != "one [31m\ntwo" {
		t.Fatalf("question = %q", got)
	}
}

func fillStatusStrings(v reflect.Value, stack map[reflect.Type]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString("a\x1b]0;x\x07b\u009bc")
		}
	case reflect.Pointer:
		if stack[v.Type()] {
			return
		}
		stack[v.Type()] = true
		if v.IsNil() && v.CanSet() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		if !v.IsNil() {
			fillStatusStrings(v.Elem(), stack)
		}
		delete(stack, v.Type())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillStatusStrings(v.Field(i), stack)
			}
		}
	case reflect.Slice:
		if v.CanSet() {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			fillStatusStrings(v.Index(0), stack)
		}
	case reflect.Array:
		for i := range v.Len() {
			fillStatusStrings(v.Index(i), stack)
		}
	}
}
