// Package machine derives a stable per-machine identifier used for cloud
// quota objects.
package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var machineIDVal string

func windowsRegValue(key, name string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	out, err := exec.Command("reg", "query", key, "/v", name).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(strings.ToLower(line), strings.ToLower(name)) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			return parts[len(parts)-1]
		}
	}
	return ""
}

func ID() string {
	if machineIDVal != "" {
		return machineIDVal
	}
	guid := windowsRegValue(`HKLM\SOFTWARE\Microsoft\Cryptography`, "MachineGuid")
	prod := windowsRegValue(`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductId")
	host, _ := os.Hostname()
	raw := strings.ToLower(strings.TrimSpace(guid) + "|" + strings.TrimSpace(prod) + "|" + strings.TrimSpace(host))
	if strings.Trim(raw, "|") == "" {
		raw = "solo|" + host
	}
	sum := sha256.Sum256([]byte("solo-checker/machine/" + raw))
	hexid := strings.ToUpper(hex.EncodeToString(sum[:8]))
	machineIDVal = hexid[:4] + "-" + hexid[4:8] + "-" + hexid[8:12] + "-" + hexid[12:]
	return machineIDVal
}
