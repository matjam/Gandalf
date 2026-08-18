// Package index stores an embedded, searchable copy of the vault's notes.
//
// The index is derived data. It lives outside the notes, can be thrown away,
// and is rebuilt from the markdown whenever it disagrees — the notes are the
// record and the index is a convenience over them.
package index

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/matjam/gandalf/internal/vault"
)

// Chunk is a searchable piece of a note.
//
// Notes are split by heading rather than indexed whole, because a match
// against one section of a long design note should return that section, not
// the whole document — and because embedding models have a context window that
// a long note will exceed.
type Chunk struct {
	// Ref addresses the note this came from.
	Ref string

	// Path is the note's vault path, for rebuilding after a note moves.
	Path string

	// Heading is the section this chunk covers, empty for a note's preamble.
	Heading string

	// Title is the note's own title, included so a result can be read without
	// a second lookup.
	Title string

	// Text is what gets embedded and searched.
	Text string

	// Hash fingerprints Text, so unchanged chunks are not re-embedded.
	Hash string
}

// maxChunkRunes bounds a chunk so a long section still fits a small model's
// context window. Sections longer than this are split on paragraph boundaries.
const maxChunkRunes = 1200

// Chunks splits a note into searchable pieces.
//
// The maintained backlinks block is excluded: it is a list of other notes'
// names, so indexing it would return this note for searches about them.
func Chunks(ref, title string, note *vault.Note) []Chunk {
	var out []Chunk

	for _, section := range sections(note.Content()) {
		for _, text := range split(section.text) {
			if strings.TrimSpace(text) == "" {
				continue
			}
			out = append(out, Chunk{
				Ref:     ref,
				Path:    note.Path,
				Heading: section.heading,
				Title:   title,
				Text:    text,
				Hash:    hash(text),
			})
		}
	}

	return out
}

// section is a heading and the text beneath it.
type section struct {
	heading string
	text    string
}

// sections splits markdown at headings, keeping each heading with its body.
func sections(body string) []section {
	var (
		out     []section
		heading string
		buf     []string
		inFence bool
	)

	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" {
			out = append(out, section{heading: heading, text: text})
		}
		buf = buf[:0]
	}

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)

		// A heading inside a fenced block is code, not structure.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			buf = append(buf, line)
			continue
		}
		if !inFence && strings.HasPrefix(trimmed, "#") {
			flush()
			heading = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			continue
		}

		buf = append(buf, line)
	}
	flush()

	return out
}

// split breaks an over-long section on paragraph boundaries.
func split(text string) []string {
	if len([]rune(text)) <= maxChunkRunes {
		return []string{text}
	}

	var (
		out     []string
		current []string
		size    int
	)

	for _, para := range strings.Split(text, "\n\n") {
		n := len([]rune(para))
		if size > 0 && size+n > maxChunkRunes {
			out = append(out, strings.Join(current, "\n\n"))
			current, size = nil, 0
		}
		current = append(current, para)
		size += n
	}
	if len(current) > 0 {
		out = append(out, strings.Join(current, "\n\n"))
	}

	return out
}

// hash fingerprints a chunk's text.
func hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}
