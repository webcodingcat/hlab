package api

import "github.com/webcodingcat/hlab/internal/labels"

// CheckLabels validates l by the Hetzner label rules; an invalid set gives
// a *FieldError on field "labels".
func CheckLabels(l map[string]string) error {
	if err := labels.Validate(l); err != nil {
		return &FieldError{Field: "labels", Message: err.Error()}
	}
	return nil
}
