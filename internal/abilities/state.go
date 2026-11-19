package abilities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func stateFilePath(scopeDir string) string {
	return filepath.Join(scopeDir, StateFileName)
}

func globalTrustStatePath(globalDir string) (string, error) {
	if globalDir == "" {
		return "", errors.New("global ability directory is not configured")
	}
	canonGlobal, err := canonicalDir(globalDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(canonGlobal, StateFileName), nil
}

func projectTrustStatePath(globalDir, projectDir string) (string, error) {
	if globalDir == "" {
		return "", errors.New("global ability directory is not configured")
	}
	if projectDir == "" {
		return "", errors.New("project ability directory is not configured")
	}
	canonGlobal, err := canonicalDir(globalDir)
	if err != nil {
		return "", err
	}
	canonProj, err := canonicalDir(projectDir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonProj))
	return filepath.Join(canonGlobal, "ability-approvals", hex.EncodeToString(sum[:]), StateFileName), nil
}

func trustStatePathForScope(globalDir, projectDir, scope string) (string, error) {
	switch scope {
	case ScopeGlobal:
		return globalTrustStatePath(globalDir)
	case ScopeProject:
		return projectTrustStatePath(globalDir, projectDir)
	default:
		return "", errors.New("invalid ability scope")
	}
}

func loadTrustStatesForScope(globalDir, projectDir, scope string) (map[string]TrustState, error) {
	if scope == ScopeProject && globalDir == "" {
		// Discovery without GlobalDir leaves project abilities quarantined.
		return map[string]TrustState{}, nil
	}
	path, err := trustStatePathForScope(globalDir, projectDir, scope)
	if err != nil {
		return map[string]TrustState{}, err
	}
	return loadTrustStatesFromPath(path)
}

func saveTrustStatesForScope(globalDir, projectDir, scope string, states map[string]TrustState) error {
	path, err := trustStatePathForScope(globalDir, projectDir, scope)
	if err != nil {
		return err
	}
	return saveTrustStatesToPath(path, states)
}

func loadTrustStatesFromPath(path string) (map[string]TrustState, error) {
	if path == "" {
		return map[string]TrustState{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]TrustState{}, nil
		}
		return map[string]TrustState{}, err
	}
	if len(raw) == 0 {
		return map[string]TrustState{}, nil
	}
	var out map[string]TrustState
	if err := json.Unmarshal(raw, &out); err != nil {
		// Fail closed: corrupt trust state leaves all abilities quarantined.
		return map[string]TrustState{}, err
	}
	if out == nil {
		out = map[string]TrustState{}
	}
	return out, nil
}

func saveTrustStates(scopeDir string, states map[string]TrustState) error {
	if scopeDir == "" {
		return errors.New("scope directory is empty")
	}
	return saveTrustStatesToPath(stateFilePath(scopeDir), states)
}

func saveTrustStatesToPath(path string, states map[string]TrustState) error {
	if path == "" {
		return errors.New("trust state path is empty")
	}
	if states == nil {
		states = map[string]TrustState{}
	}
	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
