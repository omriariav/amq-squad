package sealchain

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func rollbackRowJSONKeys(typ reflect.Type) []string {
	var keys []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" {
			key = field.Name
		}
		if key != "-" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func TestRC1SealchainSealedTypeKeyGuard(t *testing.T) {
	// Frozen at v3.2.0-rc.1 3966afb7,
	// internal/sealchain/sealchain.go:235-270. head.layout is the sole key
	// added since and is covered independently by both frozen ledger rows.
	tag := map[string]map[string]bool{
		"head": {"schema": true, "name": true, "seq": true, "link_tag": true, "seed_dev": true, "seed_ino": true, "at": true, "tag": true},
		"Link": {"seq": true, "prev": true, "tag": true},
	}
	post := map[string]map[string][]string{
		"head": {"layout": {"TestRC1WrittenCommitLedgerReadsAndMigratesOnce", "TestRC1WrittenExitsLedgerReadsAndMigratesOnce"}},
	}
	types := map[string]reflect.Type{"head": reflect.TypeOf(head{}), "Link": reflect.TypeOf(Link{})}
	var packageTests []byte
	for _, pattern := range []string{"../seatcommit/*_test.go", "../scheduler/*_test.go"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			packageTests = append(packageTests, raw...)
		}
	}
	for typeName, typ := range types {
		for _, key := range rollbackRowJSONKeys(typ) {
			if tag[typeName][key] {
				continue
			}
			rows := post[typeName][key]
			if len(rows) == 0 {
				t.Errorf("add a frozen rc.1 row for %s.%s and list it here", typeName, key)
				continue
			}
			for _, row := range rows {
				if !bytes.Contains(packageTests, []byte("func "+row+"(")) {
					t.Errorf("listed frozen rc.1 row %s for %s.%s does not exist", row, typeName, key)
				}
			}
		}
	}
}
