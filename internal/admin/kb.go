package admin

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"pcsvoip-cms/internal/db"
)

// KBRepo wraps the bbolt-backed knowledge base.
type KBRepo struct{ DB *db.DB }

func NewKBRepo(database *db.DB) *KBRepo { return &KBRepo{DB: database} }

// List returns all documents, newest first.
func (r *KBRepo) List() ([]KBDocument, error) {
	var docs []KBDocument
	err := r.DB.ForEach(db.BucketKB, func(_ string, raw []byte) error {
		var d KBDocument
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil // skip corrupt rows
		}
		docs = append(docs, d)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].CreatedAt.After(docs[j].CreatedAt)
	})
	return docs, nil
}

// ListActive returns only documents flagged active, newest first.
func (r *KBRepo) ListActive() ([]KBDocument, error) {
	all, err := r.List()
	if err != nil {
		return nil, err
	}
	out := make([]KBDocument, 0, len(all))
	for _, d := range all {
		if d.Active {
			out = append(out, d)
		}
	}
	return out, nil
}

// Get loads a document by id.
func (r *KBRepo) Get(id string) (*KBDocument, error) {
	var d KBDocument
	err := r.DB.Get(db.BucketKB, id, &d)
	if errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// Create stores a new document, populating ID and timestamps.
func (r *KBRepo) Create(d *KBDocument) error {
	if d.ID == "" {
		d.ID = NewID()
	}
	if d.Title == "" {
		d.Title = "Untitled"
	}
	d.Bytes = len(d.Content)
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	return r.DB.Put(db.BucketKB, d.ID, d)
}

// Update saves an existing document.
func (r *KBRepo) Update(d *KBDocument) error {
	d.Bytes = len(d.Content)
	d.UpdatedAt = time.Now().UTC()
	return r.DB.Put(db.BucketKB, d.ID, d)
}

// Delete removes a document.
func (r *KBRepo) Delete(id string) error {
	return r.DB.Delete(db.BucketKB, id)
}

// ToggleActive flips the Active flag.
func (r *KBRepo) ToggleActive(id string) error {
	d, err := r.Get(id)
	if err != nil {
		return err
	}
	d.Active = !d.Active
	return r.Update(d)
}

// Count returns the number of stored documents.
func (r *KBRepo) Count() (int, error) {
	return r.DB.Count(db.BucketKB)
}

// CountActive returns the number of currently active documents.
func (r *KBRepo) CountActive() (int, error) {
	docs, err := r.ListActive()
	if err != nil {
		return 0, err
	}
	return len(docs), nil
}

// ParseTags splits a comma-separated tag input.
func ParseTags(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
