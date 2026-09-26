package relay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// fpSalt 是 Command Code CLI 里 buildMachineFingerprint 的固定根盐。
//
// 它参与最终的 hashSignal 计算，不能改——改了算出来的指纹形态就和官方 CLI
// 对不上。想成批换身份要动的是 FingerprintSalt（配置项），不是这个常量。
const fpSalt = "command-code:device-fingerprint:v1"

// defaultDeviceProjectDir 是伪造的项目目录。与 x-project-slug 同源：
// 真机上 slug = slugify(workingDir)，所以两者必须来自同一个值，
// 否则会出现「slug 说 A 项目、workingDir 说 B 项目」的自相矛盾。
const defaultDeviceProjectDir = `C:\Users\dev\projects\app`

// 伪造信号的候选池。真实 CLI 读的是本机注册表/网卡/os.userInfo/git config，
// 这里按 apiKey 确定性地从池里挑，保证同一把 key 永远对应同一台"设备"。
var (
	fingerprintCPUs = []struct {
		Model string
		Cores int
	}{
		{"12th Gen Intel(R) Core(TM) i7-12650H", 10},
		{"12th Gen Intel(R) Core(TM) i5-12400F", 6},
		{"12th Gen Intel(R) Core(TM) i9-12900K", 16},
		{"13th Gen Intel(R) Core(TM) i7-13700K", 16},
		{"13th Gen Intel(R) Core(TM) i5-13600K", 14},
		{"13th Gen Intel(R) Core(TM) i9-13900K", 24},
		{"Intel(R) Core(TM) Ultra 7 155H", 16},
		{"Intel(R) Core(TM) Ultra 9 285H", 16},
		{"Intel(R) Core(TM) i9-14900K", 24},
		{"Intel(R) Core(TM) i7-14700K", 20},
		{"AMD Ryzen 7 7800X3D", 8},
		{"AMD Ryzen 9 7950X", 16},
		{"AMD Ryzen 5 7600", 6},
		{"AMD Ryzen 9 7900X", 12},
		{"AMD Ryzen 7 5800X3D", 8},
	}
	fingerprintMems = []int{8, 16, 24, 32, 48, 64}
	fingerprintTZs  = []string{
		"America/New_York", "America/Chicago", "America/Los_Angeles", "America/Toronto",
		"Europe/London", "Europe/Berlin", "Europe/Paris", "Europe/Moscow",
		"Asia/Shanghai", "Asia/Tokyo", "Asia/Singapore", "Asia/Seoul", "Asia/Hong_Kong",
		"Australia/Sydney", "Pacific/Auckland",
	}
	fingerprintMACCounts = []int{2, 3, 4, 5}
	fingerprintOSUsers   = []string{"dev", "user", "admin", "coder", "engineer", "work"}
	fingerprintMailHosts = []string{"gmail.com", "outlook.com", "qq.com", "163.com"}
)

// DeviceProfile 是伪装出来的设备档案。
type DeviceProfile struct {
	Platform    string
	Arch        string
	OSRelease   string
	IsContainer bool
	ProjectDir  string
}

// FingerprintComponents 是上报给 /alpha/fingerprint/record 的 components 块。
//
// 哈希字段用 omitempty：对应的信号为空时 CLI 会把键整个丢掉，
// 发空串和丢键在上游看来是可区分的。
type FingerprintComponents struct {
	MachineIDHash string   `json:"machineIdHash,omitempty"`
	MACHashes     []string `json:"macHashes,omitempty"`
	OSUserHash    string   `json:"osUserHash,omitempty"`
	HostnameHash  string   `json:"hostnameHash,omitempty"`
	GitEmailHash  string   `json:"gitEmailHash,omitempty"`
	Platform      string   `json:"platform"`
	Arch          string   `json:"arch"`
	OSRelease     string   `json:"osRelease"`
	CPUModel      string   `json:"cpuModel"`
	CPUCount      int      `json:"cpuCount"`
	MemGiB        int      `json:"memGiB"`
	IsContainer   bool     `json:"isContainer"`
	Timezone      string   `json:"timezone"`
	Runtime       string   `json:"runtime"`
	CollectorVer  int      `json:"collectorVersion"`
}

// Fingerprint 是完整的指纹上报体。
type Fingerprint struct {
	Thumbmark  string                `json:"thumbmark"`
	Components FingerprintComponents `json:"components"`
}

// fingerprintDeriver 按 apiKey 确定性地派生伪造信号。
type fingerprintDeriver struct {
	salt string
}

// fpDigest 返回 sha256(salt \0 apiKey \0 field) 的原始字节。
func (d fingerprintDeriver) fpDigest(apiKey, field string) []byte {
	h := sha256.New()
	h.Write([]byte(d.salt))
	h.Write([]byte{0})
	h.Write([]byte(apiKey))
	h.Write([]byte{0})
	h.Write([]byte(field))
	return h.Sum(nil)
}

// fpHex 取 fpDigest 结果的前 n 字节的十六进制。
func (d fingerprintDeriver) fpHex(apiKey, field string, n int) string {
	return hex.EncodeToString(d.fpDigest(apiKey, field)[:n])
}

// hashSignal 是 CLI 的 hashSignal：sha256(FP_SALT \0 lower(value))。
// 空值返回空串，由调用方决定是否省略该键。
func hashSignal(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fpSalt + "\x00" + v))
	return hex.EncodeToString(sum[:])
}

// pickIndex 从候选池里确定性地挑一项：对每项算 sha256 并取字典序最大者。
//
// 用「打分取最大」而不是「哈希取模」是有意的：以后往候选池里加一项，
// 只影响恰好让新项胜出的那些 key，不会像取模那样因为池长度变化
// 让所有 key 一起换设备——集体换设备本身就是异常信号。
func (d fingerprintDeriver) pickIndex(apiKey, field string, n int, labelOf func(int) string) int {
	bestIdx := 0
	var bestScore []byte
	for i := range n {
		score := d.fpDigest(apiKey, field+"\x00"+labelOf(i))
		if bestScore == nil || bytes.Compare(score, bestScore) > 0 {
			bestScore = score
			bestIdx = i
		}
	}
	return bestIdx
}

// GenerateFingerprint 为指定 key 生成设备指纹。
//
// 必须由 apiKey 派生而非随机：指纹代表「这个账号对应的那台设备」，
// 进程重启、内存回收、多实例部署、账号停用数周后恢复，上游都应看到同一台设备。
// 每次请求随机生成等于每次都在换机器，那本身就是最明显的可疑信号。
func GenerateFingerprint(apiKey, salt, deviceProjectDir string) *Fingerprint {
	d := fingerprintDeriver{salt: salt}

	cpuIdx := d.pickIndex(apiKey, "cpu", len(fingerprintCPUs), func(i int) string {
		return fingerprintCPUs[i].Model + "|" + strconv.Itoa(fingerprintCPUs[i].Cores)
	})
	cpu := fingerprintCPUs[cpuIdx]

	memIdx := d.pickIndex(apiKey, "mem", len(fingerprintMems), func(i int) string {
		return strconv.Itoa(fingerprintMems[i])
	})
	tzIdx := d.pickIndex(apiKey, "timezone", len(fingerprintTZs), func(i int) string {
		return fingerprintTZs[i]
	})
	macCountIdx := d.pickIndex(apiKey, "macCount", len(fingerprintMACCounts), func(i int) string {
		return strconv.Itoa(fingerprintMACCounts[i])
	})
	userIdx := d.pickIndex(apiKey, "osUser", len(fingerprintOSUsers), func(i int) string {
		return fingerprintOSUsers[i]
	})
	mailIdx := d.pickIndex(apiKey, "mailDomain", len(fingerprintMailHosts), func(i int) string {
		return fingerprintMailHosts[i]
	})

	// Windows MachineGuid 的形态：8-4-4-4-12。
	mid := d.fpHex(apiKey, "machineId", 16)
	machineID := mid[0:8] + "-" + mid[8:12] + "-" + mid[12:16] + "-" + mid[16:20] + "-" + mid[20:32]

	macCount := fingerprintMACCounts[macCountIdx]
	macs := make([]string, 0, macCount)
	for i := range macCount {
		raw := d.fpDigest(apiKey, "mac"+strconv.Itoa(i))[:6]
		parts := make([]string, len(raw))
		for j, b := range raw {
			parts[j] = hex.EncodeToString([]byte{b})
		}
		macs = append(macs, strings.Join(parts, ":"))
	}
	// CLI 对 MAC 去重后排序；这里生成的就是有序集合，排序保证上报顺序稳定。
	sort.Strings(macs)

	hostname := "DESKTOP-" + strings.ToUpper(d.fpHex(apiKey, "hostname", 4))
	osUser := fingerprintOSUsers[userIdx]
	gitEmail := osUser + "." + d.fpHex(apiKey, "gitEmail", 3) + "@" + fingerprintMailHosts[mailIdx]

	macHashes := make([]string, 0, len(macs))
	for _, m := range macs {
		if h := hashSignal(m); h != "" {
			macHashes = append(macHashes, h)
		}
	}

	// CLI 的 thumbmark：主盐 + "\0machine\0" + join(seed, "|")。
	// machineId 非空时不再拼 hostname/cpuModel——真机走的就是这条分支。
	seed := []string{strings.TrimSpace(machineID), strings.Join(macs, ",")}
	if strings.TrimSpace(machineID) == "" {
		seed = append(seed, hostname, cpu.Model)
	}
	seed = filterEmpty(seed)
	thumbInput := strings.Join(seed, "|")
	if thumbInput == "" {
		thumbInput = "unknown"
	}
	thumbSum := sha256.Sum256([]byte(fpSalt + "\x00machine\x00" + thumbInput))

	return &Fingerprint{
		Thumbmark: hex.EncodeToString(thumbSum[:]),
		Components: FingerprintComponents{
			MachineIDHash: hashSignal(machineID),
			MACHashes:     macHashes,
			OSUserHash:    hashSignal(osUser),
			HostnameHash:  hashSignal(hostname),
			GitEmailHash:  hashSignal(gitEmail),
			Platform:      "win32",
			Arch:          "x64",
			OSRelease:     "10.0.22631",
			CPUModel:      cpu.Model,
			CPUCount:      cpu.Cores,
			MemGiB:        fingerprintMems[memIdx],
			IsContainer:   false,
			Timezone:      fingerprintTZs[tzIdx],
			Runtime:       "cli",
			CollectorVer:  1,
		},
	}
}

// DeviceProfileFor 返回与该指纹自洽的设备档案。
//
// 单独拎出来是为了让信封里的 config.environment / workingDir 和指纹用同一份值，
// 避免「指纹说 win32、环境说 linux」这类自相矛盾——同时也就不会把宿主机的
// 真实平台、cwd 交给上游。
func DeviceProfileFor(deviceProjectDir string) DeviceProfile {
	dir := deviceProjectDir
	if dir == "" {
		dir = defaultDeviceProjectDir
	}
	return DeviceProfile{
		Platform:    "win32",
		Arch:        "x64",
		OSRelease:   "10.0.22631",
		IsContainer: false,
		ProjectDir:  dir,
	}
}

// slugifyProjectPath 复刻 CLI 的 slug 规则：对完整工作目录做 slugify。
//
// 注意 x-project-slug 用的是这个函数的结果，而不是配置里的 projectSlug——
// 上游期望 slug 与 workingDir 同源。
func slugifyProjectPath(p string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(p) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "root"
	}
	return s
}

func filterEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
