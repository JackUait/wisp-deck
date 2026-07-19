package soundpref

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

const maximumDocumentBytes = 64 * 1024

// IsAllowedName reports whether name is one of the fixed macOS system sounds
// Wisp Deck may play. A switch keeps this security boundary immutable.
func IsAllowedName(name string) bool {
	switch name {
	case "Basso", "Blow", "Bottle", "Frog", "Funk", "Glass", "Hero",
		"Morse", "Ping", "Pop", "Purr", "Sosumi", "Submarine", "Tink":
		return true
	default:
		return false
	}
}

// LoadDocument reads a small, regular preference file without following a
// blocking special-file read and applies the same strict JSON rules used by
// Read. Callers that update the document can therefore never normalize an
// ambiguous opt-in into an enabled preference.
func LoadDocument(path string) (map[string]any, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("invalid preference descriptor")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumDocumentBytes {
		return nil, errors.New("invalid preference file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumDocumentBytes || !utf8.Valid(data) {
		return nil, errors.New("invalid preference contents")
	}
	decoded, err := decodeStrictJSON(data)
	if err != nil {
		return nil, err
	}
	preference, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("preference document is not an object")
	}
	for key := range preference {
		if (key != "sound" && strings.EqualFold(key, "sound")) ||
			(key != "sound_name" && strings.EqualFold(key, "sound_name")) {
			return nil, errors.New("ambiguous preference key")
		}
	}
	return preference, nil
}

// Read returns a playable system sound only for an explicit, valid opt-in.
// Missing, incomplete, invalid, or oversized input is silent by default.
func Read(path string) string {
	preference, err := LoadDocument(path)
	if err != nil {
		return ""
	}
	enabled, ok := preference["sound"].(bool)
	if !ok || !enabled {
		return ""
	}
	value, present := preference["sound_name"]
	if !present {
		return "Bottle"
	}
	name, ok := value.(string)
	if !ok {
		return ""
	}
	if name == "" {
		return "Bottle"
	}
	if !IsAllowedName(name) {
		return "Bottle"
	}
	return name
}

// decodeStrictJSON rejects duplicate object keys at every nesting level. The
// standard struct decoder is case-insensitive and last-key-wins, both unsafe
// properties for an opt-in flag.
func decodeStrictJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeStrictValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func decodeStrictValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("non-string JSON object key")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, errors.New("duplicate JSON object key")
			}
			value, err := decodeStrictValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return object, nil
	case '[':
		var array []any
		for decoder.More() {
			value, err := decodeStrictValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return array, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

// LockPath is shared with notification-setup.sh. Keeping the lock beside the
// preference makes rename-based writes and playback linearize on one inode.
func LockPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
}

// WithExclusiveLock serializes a preference write or a read-plus-playback
// transaction. flock and macOS lockf interoperate; integration tests exercise
// both directions across real processes.
func WithExclusiveLock(path string, transaction func() error) error {
	lock, err := os.OpenFile(LockPath(path), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck -- closing the fd also releases it
	if transaction == nil {
		return errors.New("nil sound preference transaction")
	}
	return transaction()
}
