package music

import (
	"errors"
	"time"
)

var errResumeVerificationRetry = errors.New("校验失败后请等待 3 秒再重试")
var errAudioValidation = errors.New("audio validation failed")

// IDs originate from owned persistent tasks. Expired entries are discarded;
// successful previews bypass this gate, and the map has a hard upper bound.
func (s *Store) admitResumeVerification(id string, now time.Time) error {
	s.resumeVerifyMu.Lock()
	defer s.resumeVerifyMu.Unlock()
	if now.Before(s.resumeVerifyNext[id]) {
		return errResumeVerificationRetry
	}
	for id, next := range s.resumeVerifyNext {
		if !now.Before(next) {
			delete(s.resumeVerifyNext, id)
		}
	}
	if len(s.resumeVerifyNext) >= 256 {
		return errResumeBusy
	}
	if s.resumeVerifyNext == nil {
		s.resumeVerifyNext = map[string]time.Time{}
	}
	s.resumeVerifyNext[id] = now.Add(3 * time.Second)
	return nil
}

func (s *Store) finishResumeVerification(id string, failure error, now time.Time) {
	s.resumeVerifyMu.Lock()
	defer s.resumeVerifyMu.Unlock()
	if !errors.Is(failure, errResumeCorrupt) && !errors.Is(failure, errAudioValidation) {
		delete(s.resumeVerifyNext, id)
		return
	}
	// Verification itself can take minutes; the failure cooldown begins only
	// after it finishes, so large files cannot immediately trigger another read.
	if s.resumeVerifyNext == nil {
		s.resumeVerifyNext = map[string]time.Time{}
	}
	if _, exists := s.resumeVerifyNext[id]; exists || len(s.resumeVerifyNext) < 256 {
		s.resumeVerifyNext[id] = now.Add(3 * time.Second)
	}
}
