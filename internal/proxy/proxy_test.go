package proxy

import "testing"

func TestStripPrefixRespectsPathSegmentBoundaries(t *testing.T) {
	testCases := []struct {
		name   string
		prefix string
		path   string
		want   string
	}{
		{
			name:   "exact match becomes root",
			prefix: "/api",
			path:   "/api",
			want:   "/",
		},
		{
			name:   "segment match strips prefix",
			prefix: "/api",
			path:   "/api/users",
			want:   "/users",
		},
		{
			name:   "trailing slash prefix still works",
			prefix: "/api/",
			path:   "/api/users",
			want:   "/users",
		},
		{
			name:   "partial segment does not strip",
			prefix: "/api",
			path:   "/apiusers",
			want:   "/apiusers",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := stripPrefix(testCase.prefix, testCase.path); got != testCase.want {
				t.Fatalf("expected %q, got %q", testCase.want, got)
			}
		})
	}
}
