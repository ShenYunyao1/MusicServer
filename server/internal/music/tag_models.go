package music

import "errors"

var (
	errTagsUnavailable = errors.New("tags require the schema 8 database")
	errTagInvalid      = errors.New("invalid tag input")
	errTagNameTaken    = errors.New("tag name or alias already exists")
	errTagLimit        = errors.New("tag dictionary limit reached")
	errTagInUse        = errors.New("tag or group is in use")
	errTagSession      = errors.New("a current account session is required")
	errTagForbidden    = errors.New("administrator permission is required")
)

type TagGroup struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	Builtin   bool   `json:"builtin"`
	Version   int64  `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type TagGroupInput struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

type TagGroupPatch struct {
	Name      *string `json:"name"`
	SortOrder *int    `json:"sort_order"`
}

type Tag struct {
	ID         string   `json:"id"`
	GroupID    string   `json:"group_id"`
	Scope      string   `json:"scope"`
	Name       string   `json:"name"`
	Aliases    []string `json:"aliases"`
	Version    int64    `json:"version"`
	Deleted    bool     `json:"deleted"`
	MergedInto string   `json:"merged_into,omitempty"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	scopeID    string
}

type TagInput struct {
	Scope   string   `json:"scope"`
	GroupID string   `json:"group_id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

type TagPatch struct {
	Name    *string   `json:"name"`
	Aliases *[]string `json:"aliases"`
}

type TagQuery struct {
	Scope   string
	GroupID string
	Query   string
	Offset  int
	Limit   int
}

type TagPage struct {
	Items    []Tag `json:"items"`
	Total    int   `json:"total"`
	Offset   int   `json:"offset"`
	Limit    int   `json:"limit"`
	Revision int64 `json:"revision"`
}
