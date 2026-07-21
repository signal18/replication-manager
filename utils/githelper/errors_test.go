package githelper

import "testing"

func TestGitlabErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "oauth error/description",
			body: `{"error":"invalid_grant","error_description":"Invalid credentials"}`,
			want: "invalid_grant Invalid credentials",
		},
		{
			name: "message as plain string",
			body: `{"message":"403 Forbidden"}`,
			want: "403 Forbidden",
		},
		{
			name: "message as field->reasons object (path taken)",
			body: `{"message":{"path":["has already been taken"]}}`,
			want: "path has already been taken",
		},
		{
			name: "non-json body falls back to raw",
			body: `Bad Gateway`,
			want: "Bad Gateway",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitlabErrorMessage([]byte(tc.body)); got != tc.want {
				t.Fatalf("gitlabErrorMessage(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// TestGitlabErrorMessage_TakenDetectable guards the substring the create path
// relies on to raise ErrDomainAlreadyOwned.
func TestGitlabErrorMessage_TakenDetectable(t *testing.T) {
	msg := gitlabErrorMessage([]byte(`{"message":{"path":["has already been taken"]}}`))
	if want := "has already been taken"; !contains(msg, want) {
		t.Fatalf("message %q does not contain %q — ownership detection would break", msg, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
