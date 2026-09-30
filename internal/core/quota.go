// Package core holds shared helpers: daily quota accounting with cloud sync
// and small file utilities.
package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"xbox-checker/internal/keys"
	"xbox-checker/internal/machine"
	"xbox-checker/internal/r2store"
)

var (
	quotaMu     sync.Mutex
	quotaDate   string
	quotaCount  int
	quotaLoaded bool
	DailyLimit  = DefaultDailyLimit
	flushing    int32
	flushDirty  int32
)

func todayStamp() string {
	return time.Now().Format("2006-01-02")
}

func quotaKey() []byte {
	sum := sha256.Sum256([]byte("xbox-checker/daily-quota/v1"))
	return sum[:]
}

func sealQuota(date string, count int) string {
	block, err := aes.NewCipher(quotaKey())
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	plain := []byte(fmt.Sprintf("%s|%d", date, count))
	out := gcm.Seal(nonce, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(out)
}

func openQuota(blob string) (string, int, bool) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(blob))
	if err != nil {
		return "", 0, false
	}
	block, err := aes.NewCipher(quotaKey())
	if err != nil {
		return "", 0, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", 0, false
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", 0, false
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", 0, false
	}
	parts := strings.SplitN(string(plain), "|", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	n := 0
	fmt.Sscanf(parts[1], "%d", &n)
	if n < 0 {
		n = 0
	}
	return parts[0], n, true
}

func dropLocalEnv() {
	_ = os.Remove(DailyEnvFile)
	_ = os.Remove(".env")
}

func NextResetTime() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}

func LimitMessage() string {
	reset := NextResetTime()
	return fmt.Sprintf("Daily valid limit reached (%d). Continue when your daily limit refreshes at %s (%s).",
		DailyLimit, reset.Format("3:04 PM"), reset.Format("2006-01-02"))
}

func persistQuotaLocked() {
	scheduleCloudFlushLocked()
}

func loadQuotaLocked() {
	if quotaLoaded && quotaDate == todayStamp() {
		return
	}
	today := todayStamp()
	if quotaDate != today {
		quotaCount = 0
	}
	quotaDate = today
	quotaLoaded = true
}

func rollQuotaDayLocked() {
	loadQuotaLocked()
	today := todayStamp()
	if quotaDate != today {
		quotaDate = today
		quotaCount = 0
		scheduleCloudFlushLocked()
	}
}

func dailyUsed() int {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	rollQuotaDayLocked()
	return quotaCount
}

func AtLimit() bool {
	return dailyUsed() >= DailyLimit
}

func AddDailyValid() int {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	rollQuotaDayLocked()
	if quotaCount < DailyLimit {
		quotaCount++
		scheduleCloudFlushLocked()
	}
	return quotaCount
}

type userCloudQuota struct {
	ID      string `json:"id"`
	Limit   int    `json:"daily_limit"`
	Date    string `json:"date"`
	Used    int    `json:"used"`
	Updated int64  `json:"updated"`
}

func userObjectKey() string {
	return keys.UsersPrefix + machine.ID() + ".json"
}

func scheduleCloudFlushLocked() {
	atomic.StoreInt32(&flushDirty, 1)
	if !atomic.CompareAndSwapInt32(&flushing, 0, 1) {
		return
	}
	go func() {
		defer atomic.StoreInt32(&flushing, 0)
		for {
			atomic.StoreInt32(&flushDirty, 0)
			quotaMu.Lock()
			snap := userCloudQuota{
				ID:      machine.ID(),
				Limit:   DailyLimit,
				Date:    quotaDate,
				Used:    quotaCount,
				Updated: time.Now().Unix(),
			}
			quotaMu.Unlock()
			b, err := json.Marshal(snap)
			if err != nil {
				return
			}
			_ = r2store.Put(userObjectKey(), b, "application/json")
			if atomic.LoadInt32(&flushDirty) == 0 {
				return
			}
		}
	}()
}

func PullQuotaFromCloud() {
	dropLocalEnv()
	id := machine.ID()
	today := todayStamp()
	b, err := r2store.Get(userObjectKey())
	if err != nil {
		quotaMu.Lock()
		quotaDate = today
		quotaLoaded = true
		snap := userCloudQuota{ID: id, Limit: DailyLimit, Date: today, Used: quotaCount, Updated: time.Now().Unix()}
		quotaMu.Unlock()
		raw, _ := json.Marshal(snap)
		_ = r2store.Put(userObjectKey(), raw, "application/json")
		return
	}
	var remote userCloudQuota
	if json.Unmarshal(b, &remote) != nil {
		return
	}
	quotaMu.Lock()
	if remote.Limit > 0 {
		DailyLimit = remote.Limit
	}
	if remote.Date == today {
		quotaDate = today
		quotaCount = remote.Used
		if quotaCount < 0 {
			quotaCount = 0
		}
	} else {
		quotaDate = today
		quotaCount = 0
	}
	quotaLoaded = true
	quotaMu.Unlock()
	if remote.Date != today {
		scheduleCloudFlushLocked()
	}
}

func nextResetTime() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}

func limitMessage() string {
	reset := nextResetTime()
	return fmt.Sprintf("Daily valid limit reached (%d). Continue when your daily limit refreshes at %s (%s).",
		DailyLimit, reset.Format("3:04 PM"), reset.Format("2006-01-02"))
}

func DailyUsed() int {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	rollQuotaDayLocked()
	return quotaCount
}
