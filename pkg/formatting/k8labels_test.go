package formatting

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

func TestK8LabelsCleanup(t *testing.T) {
	tests := []struct {
		name string
		str  string
		want string
	}{
		{
			name: "clean characters for k8 labels",
			str:  "foo/bar h#@?!ello",
			want: "foo-bar_hello",
		},
		{
			name: "keep dash",
			str:  "foo-bar-hello",
			want: "foo-bar-hello",
		},
		{
			name: "github bot name",
			str:  "github-actions[bot]",
			want: "github-actions__bot",
		},
		{
			name: "trailing dash name removed",
			str:  "MBAPPEvsMESSI--",
			want: "MBAPPEvsMESSI",
		},
		{
			name: "remove new line",
			str:  "foo\n",
			want: "foo",
		},
		{
			name: "remove new line from the middle",
			str:  "foo\nbar",
			want: "foobar",
		},
		{
			name: "secret name longer than 63 characters",
			str:  strings.Repeat("a", 64),
			want: strings.Repeat("a", 63),
		},
		{
			name: "secret name ends with non-alphanumeric character",
			str:  "secret-name-",
			want: "secret-name",
		},
		{
			name: "secret name starts with non-alphanumeric character",
			str:  "-secret-name",
			want: "secret-name",
		},
		{
			name: "secret name contains non-alphanumeric characters keep underscore",
			str:  "secret:name/with_underscores",
			want: "secret-name-with_underscores",
		},
		{
			name: "has an invalid start (.)",
			str:  ".i-start-with-an-invalid-char",
			want: "i-start-with-an-invalid-char",
		},
		{
			name: "has an invalid end (.)",
			str:  "i-end-with-an-invalid-char.",
			want: "i-end-with-an-invalid-char",
		},
		{
			name: "has an invalid start (:)",
			str:  ":i-start-with-an-invalid-char",
			want: "i-start-with-an-invalid-char",
		},
		{
			name: "has an invalid end (:)",
			str:  "i-end-with-an-invalid-char:",
			want: "i-end-with-an-invalid-char",
		},
		{
			name: "has an invalid start (/)",
			str:  "/i-start-with-an-invalid-char",
			want: "i-start-with-an-invalid-char",
		},
		{
			name: "has an invalid end (/)",
			str:  "i-end-with-an-invalid-char/",
			want: "i-end-with-an-invalid-char",
		},
		{
			name: "has an invalid start (-)",
			str:  "-i-start-with-an-invalid-char",
			want: "i-start-with-an-invalid-char",
		},
		{
			name: "has an invalid end (-)",
			str:  "i-end-with-an-invalid-char-",
			want: "i-end-with-an-invalid-char",
		},
		{
			name: "has an invalid start ( )",
			str:  " i-start-with-an-invalid-char",
			want: "i-start-with-an-invalid-char",
		},
		{
			name: "has an invalid end ( )",
			str:  "i-end-with-an-invalid-char ",
			want: "i-end-with-an-invalid-char",
		},
		{
			name: "has an invalid start ([)",
			str:  "[i-start-with-an-invalid-char",
			want: "i-start-with-an-invalid-char",
		},
		{
			name: "has an invalid end ([)",
			str:  "i-end-with-an-invalid-char[",
			want: "i-end-with-an-invalid-char",
		},
		{
			name: "has an invalid start (])",
			str:  "]i-start-with-an-invalid-char",
			want: "i-start-with-an-invalid-char",
		},
		{
			name: "has an invalid end (])",
			str:  "i-end-with-an-invalid-char]",
			want: "i-end-with-an-invalid-char",
		},
		{
			name: "long value once cut starts with a .",
			str:  "dropped." + strings.Repeat("a", 62),
			want: strings.Repeat("a", 62),
		},
		{
			name: "ones with special chars won't get longer",
			str:  strings.Repeat("[exp-63]", 10),
			want: "63____exp-63____exp-63____exp-63____exp-63____exp-63____exp-63",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanValueKubernetes(tt.str)
			validationErrs := validation.IsValidLabelValue(got)

			switch {
			case len(got) > validation.LabelValueMaxLength:
				t.Errorf("K8LabelsCleanup() = %v, produced string is too long (%v/%v)", got, len(got), validation.LabelValueMaxLength)
			case len(validationErrs) > 0:
				t.Errorf("K8LabelsCleanup() = %v, is not a valid label: %v", got, validationErrs)
			case got != tt.want:
				t.Errorf("K8LabelsCleanup() = given %v, got %v, want %v", tt.str, got, tt.want)
			}
		})
	}
}
