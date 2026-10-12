package tts

// Key states for a settings transfer (Settings → Дані → export / import,
// config/export.go): the tts-keys.json document taken from memory, and the
// records of another computer merged back in. Key ids are sha-256 of the
// provider and the key, so they match on any computer; the document never
// holds a key, only ids and masks.

import (
	"encoding/json"
	"errors"
)

// KeyStateJSON is the current key states as a tts-keys.json document (also
// for an in-memory pool). Records not seen for months are left out, like in
// the file.
func (c *Client) KeyStateJSON() ([]byte, error) { return c.pool.export() }

// ImportKeyState takes the records of a key-state document (KeyStateJSON of
// another computer). replace: a record of the document wins for a key this
// computer knows too; otherwise only keys without a record here take it.
// It returns how many records were taken. The provider fingerprints are not
// taken: the key lists of this computer decide when failing keys get another
// chance.
func (c *Client) ImportKeyState(data []byte, replace bool) (int, error) {
	return c.pool.importDoc(data, replace)
}

func (p *pool) export() ([]byte, error) {
	p.mu.Lock()
	t := p.now()
	doc := stateDoc{Version: 1, Keys: map[string]*keyRec{}}
	for id, r := range p.recs {
		if last := maxTime(maxTime(r.LastUsed, r.Until), r.Checked); !last.IsZero() && t.Sub(last) > keepStale {
			continue
		}
		cp := *r
		cp.strikes = 0
		doc.Keys[id] = &cp
	}
	p.mu.Unlock()
	return json.Marshal(doc)
}

func (p *pool) importDoc(data []byte, replace bool) (int, error) {
	var doc stateDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, errors.New("стан ключів озвучки у файлі пошкоджено")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for id, r := range doc.Keys {
		if r == nil || id == "" || (p.recs[id] != nil && !replace) {
			continue
		}
		switch r.State {
		case KeyOK, KeyExhausted, KeyError, KeyResting:
		default:
			r.State = KeyOK
		}
		cp := *r
		cp.strikes = 0
		p.recs[id] = &cp
		n++
	}
	if n > 0 {
		p.saveLocked(true)
		p.wakeLocked()
	}
	return n, nil
}
