package config

// Settings changed by another process. Two studio windows (a second `go run`,
// an old build left open) each keep the settings in memory and used to write
// the whole file on every change — the one that saved last silently undid the
// other's changes (keys added in one window vanished, a provider picked in one
// came back in the other). Now a change is applied on top of the file as it
// is on disk (Update reads it first when it changed since this store last
// read or wrote it), and Refresh lets a screen pick up what the other window
// saved.

import (
	"crypto/sha256"
	"encoding/json"
	"os"
)

// diskSum is the SHA-256 of config.json (zero when it cannot be read).
func diskSum() [32]byte {
	b, err := os.ReadFile(Path())
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(b)
}

// adoptDisk makes the file's settings current when another process changed
// it since this store last read or wrote it (a broken or unreadable file is
// ignored: ours stay). Caller holds saveMu.
func (s *Store) adoptDisk() bool {
	b, err := os.ReadFile(Path())
	if err != nil {
		return false
	}
	sum := sha256.Sum256(b)
	if sum == s.disk {
		return false
	}
	c := Default()
	if json.Unmarshal(b, &c) != nil {
		return false
	}
	c.Normalize()
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	s.disk = sum
	return true
}

// Refresh picks up config.json when another process changed it and tells the
// subscribers; false = nothing changed (in-memory stores never read the disk).
func (s *Store) Refresh() bool {
	if !s.persist {
		return false
	}
	s.saveMu.Lock()
	changed := s.adoptDisk()
	s.saveMu.Unlock()
	if !changed {
		return false
	}
	c := s.Get()
	s.mu.RLock()
	subs := append([]func(Config){}, s.subs...)
	s.mu.RUnlock()
	for _, f := range subs {
		f(c)
	}
	return true
}
