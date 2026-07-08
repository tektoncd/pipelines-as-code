package formatting

import (
	"github.com/hako/durafmt"
	"github.com/jonboulle/clockwork"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Age(t *metav1.Time, c clockwork.Clock) string {
	if t.IsZero() {
		return nonAttributedStr
	}
	return durafmt.ParseShort(c.Since(t.Time)).String() + " ago"
}

func Duration(t1, t2 *metav1.Time) string {
	if t1.IsZero() || t2.IsZero() {
		return nonAttributedStr
	}
	return durafmt.ParseShort(t2.Sub(t1.Time)).String()
}

func PRDuration(startTime, completionTime *metav1.Time) string {
	if startTime == nil || completionTime == nil {
		return nonAttributedStr
	}
	return Duration(startTime, completionTime)
}

func Timeout(t *metav1.Duration) string {
	if t == nil {
		return nonAttributedStr
	}
	return durafmt.Parse(t.Duration).String()
}
