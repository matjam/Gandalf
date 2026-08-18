package vault

import (
	"fmt"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
)

// NewNoteRequest describes a note to create. Everything the schema requires is
// either supplied here or filled in by Gandalf; callers never write a
// frontmatter block themselves.
type NewNoteRequest struct {
	Type  schema.NoteType
	Title string

	// Scope names the project a design or decisions note belongs to.
	Scope string

	// Slug overrides the slug derived from the title.
	Slug string

	// Path files the note explicitly, bypassing the layout's routing.
	Path string

	Tags    []string
	Related []string
	Author  schema.Author
	Status  schema.Status

	// Body replaces the type's template. The title heading is added when the
	// body does not already open with one.
	Body string

	// On is the note's creation date. It defaults to today.
	On schema.Date
}

// NewNote builds a note from a request: routing its path, generating valid
// frontmatter, and filling in the template for its type. The note is returned
// rather than written, so a caller can inspect it or refuse to overwrite.
func (v *Vault) NewNote(req NewNoteRequest) (*Note, error) {
	if !req.Type.Valid() {
		return nil, fmt.Errorf("unknown note type %q", req.Type)
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("a note needs a title")
	}

	on := req.On
	if on.IsZero() {
		on = schema.Today()
	}

	author := req.Author
	if author == "" {
		author = schema.AuthorAgent
	}
	if !author.Valid() {
		return nil, fmt.Errorf("unknown author %q", req.Author)
	}
	if req.Status != "" && !req.Status.Valid() {
		return nil, fmt.Errorf("unknown status %q", req.Status)
	}

	notePath := req.Path
	if notePath == "" {
		slug := req.Slug
		if slug == "" {
			slug = Slugify(req.Title)
		}
		if slug == "" {
			return nil, fmt.Errorf("title %q produces an empty slug; pass an explicit slug", req.Title)
		}
		var err error
		if notePath, err = v.layout.Path(req.Type, req.Scope, slug, on); err != nil {
			return nil, err
		}
	}

	related := make([]string, 0, len(req.Related))
	for _, r := range req.Related {
		if target := LinkTarget(r); target != "" {
			related = append(related, target)
		}
	}

	note := &Note{
		Path: notePath,
		FM: schema.Frontmatter{
			Type:    req.Type,
			Created: on,
			Updated: on,
			Tags:    req.Tags,
			Related: related,
			Author:  author,
			Status:  req.Status,
		},
		Body: body(req),
	}

	return note, nil
}

// body returns the note's starting content: the caller's own text when given,
// otherwise the template for its type. Either way it opens with an H1.
func body(req NewNoteRequest) string {
	heading := "# " + strings.TrimSpace(req.Title)

	if custom := strings.TrimSpace(req.Body); custom != "" {
		if strings.HasPrefix(custom, "# ") {
			return custom + "\n"
		}
		return heading + "\n\n" + custom + "\n"
	}

	return heading + "\n" + template(req.Type)
}

// template returns the skeleton sections for a note type. The headings exist
// to tell an agent what the note is for; empty ones are meant to be replaced,
// not preserved.
func template(t schema.NoteType) string {
	switch t {
	case schema.TypeSession:
		return `
## Goal

What this work is trying to achieve, and why.

## Context

Prior work, constraints, and anything that would be hard to reconstruct later.

## Decisions

Decisions taken and the reasoning behind them, including alternatives rejected.

## Notes

Assumptions that may need revisiting; verified state versus intended behaviour.
`
	case schema.TypeDesign:
		return `
## Overview

Current state only. Remove history rather than letting it accumulate here.

## Architecture

## Interfaces

## Open Questions
`
	case schema.TypeDecisions:
		return `
Append-only. Record significant decisions as they are made, newest last, each
with its context and the tradeoffs accepted.
`
	case schema.TypeStandard:
		return `
## Rules

## Rationale
`
	case schema.TypeGlossary:
		return `
Terms and their meanings in this vault.
`
	case schema.TypeMeeting, schema.TypeInterview:
		return `
## Attendees

## Notes

## Actions
`
	default:
		return "\n"
	}
}
