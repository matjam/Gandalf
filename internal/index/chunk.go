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

// Chunks splits a note into searchable pieces that fit within budget runes.
//
// The budget comes from the embedding model rather than a constant, because
// models differ by an order of magnitude — 256 tokens for a small sentence
// encoder against 8192 for a larger one — and a chunk that overruns is
// truncated silently, embedding the first half of a section as though it were
// the whole thing.
//
// The maintained backlinks block is excluded: it is a list of other notes'
// names, so indexing it would return this note for searches about them.
func Chunks(ref, title string, note *vault.Note, budget int) []Chunk {
	var out []Chunk

	for _, section := range sections(note.Content()) {
		for _, text := range split(section.text, budget) {
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

// split breaks an over-long section into pieces within budget.
//
// Paragraph boundaries are preferred, but a single paragraph longer than the
// budget is cut anyway: leaving it whole would hand the model text it silently
// truncates, which is worse than a chunk that ends mid-thought.
func split(text string, budget int) []string {
	if budget <= 0 {
		budget = 1200
	}
	if len([]rune(text)) <= budget {
		return []string{text}
	}

	var (
		out     []string
		current []string
		size    int
	)

	flush := func() {
		if len(current) > 0 {
			out = append(out, strings.Join(current, "\n\n"))
			current, size = nil, 0
		}
	}

	for _, para := range strings.Split(text, "\n\n") {
		for _, piece := range hardSplit(para, budget) {
			n := len([]rune(piece))
			if size > 0 && size+n > budget {
				flush()
			}
			current = append(current, piece)
			size += n
		}
	}
	flush()

	return out
}

// hardSplit cuts a paragraph that exceeds the budget on its own.
func hardSplit(para string, budget int) []string {
	runes := []rune(para)
	if len(runes) <= budget {
		return []string{para}
	}

	var out []string
	for start := 0; start < len(runes); start += budget {
		end := min(start+budget, len(runes))
		out = append(out, string(runes[start:end]))
	}
	return out
}

// hash fingerprints a chunk's text.
func hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}
