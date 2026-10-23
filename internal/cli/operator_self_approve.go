package cli

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func validateAuthorizationRequestNamespace(projectDir, profile, session string, request operatorauth.GateRequestContext) error {
	want := operatorauth.NamespaceBinding{ProjectDir: projectDir, Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session), Generation: namespaceGenerationNone}
	if request.Namespace != want {
		return fmt.Errorf("binding %+v does not match current %+v", request.Namespace, want)
	}
	return nil
}

func selfApprovalStoreDir(projectDir, profile, session string) string {
	return filepath.Join(projectDir, team.DirName, "evidence", profile, session, "self-operator")
}

func authorizationArtifactIdentity(parts ...string) string {
	h := sha256.New()
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func selfApprovalReceiptPath(projectDir, profile, session, gate, questionID, answerID string) string {
	id := authorizationArtifactIdentity("receipt-v2", gate, questionID, answerID)
	return filepath.Join(selfApprovalStoreDir(projectDir, profile, session), "authorization-v2-"+id+".receipt.json")
}

func writeImmutableEvidence(path string, b []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(b) {
			return fmt.Errorf("immutable evidence collision at %s", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWriteJSONBytes(path, b)
}

func atomicWriteJSONBytes(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".atomic-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func writeSelfApprovalReceipt(projectDir, profile, session, gate, answerID string, receipt operatorauth.Receipt) error {
	if receipt.Gate != gate || receipt.QuestionMessageID == "" || receipt.AnswerMessageID != answerID {
		return fmt.Errorf("receipt tuple does not match requested artifact identity")
	}
	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := selfApprovalReceiptPath(projectDir, profile, session, gate, receipt.QuestionMessageID, answerID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return flock.WithLock(path+".lock", func() error { return writeImmutableEvidence(path, b) })
}
