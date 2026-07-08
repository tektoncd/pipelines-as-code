// Copyright © 2020 The Tekton Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package formatting

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTimeout(t *testing.T) {
	t1 := metav1.Duration{
		Duration: 5 * time.Minute,
	}

	str := Timeout(&t1) // Timeout is defined
	assert.Equal(t, str, "5 minutes")

	str = Timeout(nil) // Timeout is not defined
	assert.Equal(t, str, nonAttributedStr)
}

func TestAge(t *testing.T) {
	clock := clockwork.NewFakeClock()
	assert.Equal(t, Age(&metav1.Time{}, clock), nonAttributedStr)

	t1 := &metav1.Time{
		Time: clock.Now().Add(-5 * time.Minute),
	}
	assert.Equal(t, Age(t1, clock), "5 minutes ago")
}

func TestDuration(t *testing.T) {
	assert.Equal(t, Duration(&metav1.Time{}, &metav1.Time{}), nonAttributedStr)
	clock := clockwork.NewFakeClock()

	assert.Equal(t, Duration(&metav1.Time{
		Time: clock.Now(),
	}, &metav1.Time{
		Time: clock.Now().Add(5 * time.Minute),
	}), "5 minutes")
}

func TestPRDuration(t *testing.T) {
	clock := clockwork.NewFakeClock()
	infiveminutes := clock.Now().Add(time.Duration(5 * int(time.Minute)))
	now := clock.Now()
	tests := []struct {
		name           string
		startTime      *metav1.Time
		completionTime *metav1.Time
		want           string
	}{
		{
			name: "no start time",
			want: nonAttributedStr,
		},
		{
			name:           "no completion time",
			startTime:      &metav1.Time{Time: now},
			completionTime: nil,
			want:           nonAttributedStr,
		},
		{
			name:           "with both times",
			startTime:      &metav1.Time{Time: now},
			completionTime: &metav1.Time{Time: infiveminutes},
			want:           "5 minutes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PRDuration(tt.startTime, tt.completionTime); got != tt.want {
				t.Errorf("PRDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}
