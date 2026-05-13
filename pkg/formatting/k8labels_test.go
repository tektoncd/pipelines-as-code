package formatting

import (
	"strings"
	"testing"
)

func TestK8LabelsCleanup(t *testing.T) {
	tests := []struct {
		name string
		str  string
		want string
	}{
		{
			name: "clean characters for k8 labels",
			str:  "foo/bar hello",
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
			name: "secret name longer than 63 characters",
			str:  strings.Repeat("a", 64),
			want: strings.Repeat("a", 62),
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
			name: "starts with a .",
			str:  ".i-start-with-a-dot",
			want: "i-start-with-a-dot",
		},
		{
			name: "ends with a .",
			str:  "i-end-with-a-dot.",
			want: "i-end-with-a-dot",
		},
		{
			name: "long value once cut starts with a .",
			str:  "everythingbeforethedotisdropped.thesehereare61chars-62withdot-previouscharacterswillbedropped",
			want: "thesehereare61chars-62withdot-previouscharacterswillbedropped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanValueKubernetes(tt.str); got != tt.want {
				t.Errorf("K8LabelsCleanup() = %v, want %v", got, tt.want)
			}
		})
	}
}
