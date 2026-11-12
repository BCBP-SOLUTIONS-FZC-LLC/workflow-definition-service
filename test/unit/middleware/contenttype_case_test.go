package middleware_test

import (
	"net/http"
	"testing"
)

func TestRequireJSONContentType_MediaTypeIsComparedWithoutRegardToCase(t *testing.T) {
	r := contentTypeRouter()
	tests := []struct {
		name        string
		contentType string
		want        int
	}{
		{"mixed case", "Application/JSON", http.StatusOK},
		{"upper case", "APPLICATION/JSON", http.StatusOK},
		{"mixed case with charset", "Application/Json; charset=UTF-8", http.StatusOK},
		{"mixed case with surrounding spaces", "  Application/JSON  ; charset=utf-8", http.StatusOK},
		{"lookalike type is still refused", "Application/JSONP", http.StatusUnsupportedMediaType},
		{"other subtype is still refused", "Application/XML", http.StatusUnsupportedMediaType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _ := doCT(r, http.MethodPost, `{"a":1}`, tt.contentType); code != tt.want {
				t.Fatalf("Content-Type %q: got %d, want %d", tt.contentType, code, tt.want)
			}
		})
	}
}
