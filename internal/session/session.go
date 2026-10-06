// Package session keeps one receiver under watch: it finds the device,
// opens its control interface, decodes the status stream it pushes, and sends
// settings back. The window only ever reads the snapshot this package keeps,
// so a device that appears, disappears or reboots never blocks the UI.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/usb"
)

// Options configure a session.
type Options struct {
	// PollInterval is how often the device tree is scanned while no
	// connection is open. The receiver is a USB device: it appears, is
	// unplugged and reboots, so the app watches rather than opens once.
	PollInterval time.Duration
	// AckTimeout is how long a command waits for the receiver's
	// acknowledgement.
	AckTimeout time.Duration
	// OnChange is called whenever the snapshot changes. It may be called
	// from any goroutine and should return quickly.
	OnChange func()
}

// LogLine is one entry of the session's activity log.
type LogLine struct {
	At    time.Time
	Text  string
	Error bool
}

// Snapshot is everything the window draws from.
type Snapshot struct {
	// Time the snapshot was taken.
	Time time.Time

	// Status is the USB device tree as last scanned.
	Status usb.Status
	// Model is the receiver family the USB ids matched.
	Model     duml.Model
	HaveModel bool

	// Connected is true while the control interface is open.
	Connected bool
	// State is the decoded receiver state, which also carries the protocol
	// version once a status frame has identified it.
	State duml.State

	// FramesPerSecond is the status stream's recent rate, in frames per
	// second: the receiver pushes about ten.
	FramesPerSecond float64
	// LastFrame is when the newest status frame arrived.
	LastFrame time.Time

	// ConnectionError explains why the receiver is not connected, when it
	// is not: no driver, the interface missing, or an open failure.
	ConnectionError string
	// Notes are diagnostics from opening the device.
	Notes []string

	// Log is the recent activity log, newest last.
	Log []LogLine
	// Installing is true while a driver install or removal runs.
	Installing bool
	// InstallLog is the output of the running or last install.
	InstallLog []string
	// DriverLogPath is the file the last install wrote its output to, which
	// is what to read when an install failed.
	DriverLogPath string
	// UnsignedRefused is true when Windows refused the driver package for
	// want of a signature, which needs the user's help: an unsigned package
	// cannot be installed by this app on such a machine.
	UnsignedRefused bool
	// ActivityLogPath is the file the activity log is copied to.
	ActivityLogPath string
	// Tools reports which optional helper programs the app found.
	Tools Tools
}

// Tools reports the helper programs found on this machine. wdi-simple.exe is
// libwdi's command line installer, whose packages Windows accepts without
// further work; Zadig is its graphical counterpart. SDKTools is true when the
// Windows SDK's makecat and signtool are present, which is what lets the app
// sign a package it writes itself.
type Tools struct {
	WdiSimple    string
	WdiSimpleSet bool
	Zadig        string
	ZadigSet     bool
	Elevated     bool
	// SignedPackage is true when this build carries a driver package signed
	// at build time, which is the route that asks nothing of the user beyond
	// the administrator prompt.
	SignedPackage bool
	// SDKTools is true when the Windows SDK's makecat and signtool are here,
	// which is what the developer route signs with.
	SDKTools bool
}

// Session watches one receiver.
type Session struct {
	opts Options

	mu       sync.Mutex
	snapshot Snapshot
	conn     *usb.Conn
	seq      uint16
	pending  map[uint16]chan struct{}
	frames   int
	rate     float64
	stop     chan struct{}
	stopped  bool
	tools    Tools

	// configPath remembers a helper program the user picked.
	configPath string
	// dataDir is where logs are kept; empty means the system's temp
	// directory.
	dataDir string

	// lastKind is the device-tree state the previous scan found, so a change
	// can be logged once rather than on every tick.
	lastKind  stateKind
	kindKnown bool
}

// New returns a session that has not been started yet.
func New(opts Options) *Session {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 1500 * time.Millisecond
	}
	if opts.AckTimeout <= 0 {
		opts.AckTimeout = 1500 * time.Millisecond
	}
	s := &Session{
		opts:    opts,
		pending: map[uint16]chan struct{}{},
		stop:    make(chan struct{}),
	}
	s.snapshot = Snapshot{
		Time:  time.Now(),
		State: duml.NewState(),
	}
	s.snapshot.Log = nil
	return s
}

// SetOnChange sets the callback that runs when the snapshot changes. It is
// set after the window exists, since it redraws it.
func (s *Session) SetOnChange(fn func()) {
	s.mu.Lock()
	s.opts.OnChange = fn
	s.mu.Unlock()
}

// SetConfigPath tells the session where to remember a chosen helper program,
// and where to keep the logs it writes.
func (s *Session) SetConfigPath(path string) {
	s.mu.Lock()
	s.configPath = path
	s.dataDir = filepath.Dir(path)
	s.mu.Unlock()
}

// Start begins watching the device in the background.
func (s *Session) Start() {
	s.refreshTools()
	go s.watch()
	go s.measureRate()
}

// Stop closes the device and ends the watch.
func (s *Session) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	close(s.stop)
	if conn != nil {
		conn.Close()
	}
}

// Snapshot returns the current state for drawing.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snapshot
	snap.Time = time.Now()
	snap.Log = append([]LogLine(nil), s.snapshot.Log...)
	snap.Notes = append([]string(nil), s.snapshot.Notes...)
	snap.InstallLog = append([]string(nil), s.snapshot.InstallLog...)
	return snap
}

// Rescan asks for an immediate scan of the device tree.
func (s *Session) Rescan() {
	go s.tick()
}

// Log appends a line to the activity log, which the driver page shows.
func (s *Session) Log(format string, a ...any) {
	s.logf(false, format, a...)
}

// logf appends a line, marking failures so the UI can colour them. The same
// line goes to the activity log file, so a report can be sent without the
// window having to be open at the time.
func (s *Session) logf(isError bool, format string, a ...any) {
	line := LogLine{At: time.Now(), Text: fmt.Sprintf(format, a...), Error: isError}
	s.mu.Lock()
	log := append(s.snapshot.Log, line)
	if len(log) > 200 {
		log = log[len(log)-200:]
	}
	s.snapshot.Log = log
	path := s.activityPath()
	s.mu.Unlock()
	s.appendLogFile(path, line)
	s.changed()
}

// activityPath is the file the activity log is mirrored to, created on first
// use.
func (s *Session) activityPath() string {
	if s.snapshot.ActivityLogPath != "" {
		return s.snapshot.ActivityLogPath
	}
	dir := s.dataDir
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "logs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.snapshot.ActivityLogPath = ""
		return ""
	}
	s.snapshot.ActivityLogPath = filepath.Join(dir, "activity.log")
	return s.snapshot.ActivityLogPath
}

func (s *Session) appendLogFile(path string, line LogLine) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", line.At.Format("2006-01-02 15:04:05"), line.Text)
}

func (s *Session) changed() {
	if s.opts.OnChange != nil {
		s.opts.OnChange()
	}
}

// watch scans the device tree and keeps a connection open when it can.
func (s *Session) watch() {
	s.tick()
	ticker := time.NewTicker(s.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// measureRate turns the raw frame counter into a per-second rate.
func (s *Session) measureRate() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := 0
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			now := s.frames
			s.rate = float64(now-last) * 1.0
			last = now
			s.snapshot.FramesPerSecond = s.rate
			s.mu.Unlock()
		}
	}
}

// tick performs one scan and opens or closes the connection accordingly.
func (s *Session) tick() {
	status, err := usb.Scan()
	if err != nil {
		s.mu.Lock()
		s.snapshot.ConnectionError = "枚举设备失败：" + err.Error()
		s.mu.Unlock()
		s.changed()
		return
	}

	model, haveModel := status.Model()
	kind := stateOf(status)

	s.mu.Lock()
	s.snapshot.Status = status
	s.snapshot.Model = model
	s.snapshot.HaveModel = haveModel

	ready := kind == stateReady
	conn := s.conn
	reason := describe(kind)
	// The interface coming and going is the one thing about this device
	// that changes on its own, so the log says so each time it happens
	// rather than silently changing state.
	changed := !s.kindKnown || kind != s.lastKind
	s.lastKind, s.kindKnown = kind, true
	s.mu.Unlock()

	if changed && kind != stateReady {
		s.logf(false, "%s", transition(kind))
	}

	switch {
	case ready && conn == nil:
		s.open(status, model)
	case !ready && conn != nil:
		// The device went away, or its interface lost its driver.
		s.close("接收器已断开或驱动已移除")
	default:
		s.mu.Lock()
		if s.snapshot.ConnectionError == "" || !ready {
			s.snapshot.ConnectionError = reason
		}
		s.mu.Unlock()
		s.changed()
	}
}

// transition is the log line for a change in what the device tree looks like,
// which is how a person watching the log learns that the interface only shows
// up after a fresh enumeration.
func transition(kind stateKind) string {
	switch kind {
	case stateNoDevice:
		return "没有检测到接收器"
	case stateUnknownModel:
		return "检测到 DJI 设备，但型号不在接收器表里"
	case stateMissingInterface:
		return "接收器在，但厂商接口不在设备树上（拔插接收器后会自动出现）"
	case stateNoDriver:
		return "厂商接口出现了，但还没有绑定 WinUSB 驱动"
	case stateWrongDriver:
		return "厂商接口绑定了别的驱动，需要换成 WinUSB"
	}
	return ""
}

// describe turns a scan into something to show a person.
func describe(kind stateKind) string {
	switch kind {
	case stateNoDevice:
		return "没有检测到接收器：请用 USB-C 线把接收器连到电脑。"
	case stateUnknownModel:
		return "检测到 DJI 设备，但它的 USB 标识不在已知接收器表里，程序不会贸然给它装驱动。"
	case stateMissingInterface:
		return "接收器已连接，但它的厂商接口（接口 6）还没有出现。装上驱动后请重新插拔接收器。"
	case stateNoDriver:
		return "厂商接口还没有绑定 WinUSB 驱动，所以收不到麦克风状态。"
	case stateWrongDriver:
		return "厂商接口绑定的是别的驱动，需要换成 WinUSB。"
	}
	return ""
}

type stateKind int

const (
	stateReady stateKind = iota
	stateNoDevice
	stateUnknownModel
	stateMissingInterface
	stateNoDriver
	stateWrongDriver
)

// stateOf classifies a scan.
func stateOf(status usb.Status) stateKind {
	if !status.Connected() {
		return stateNoDevice
	}
	if _, known := status.Model(); !known {
		return stateUnknownModel
	}
	control := status.Control
	if control == nil || !control.Present {
		return stateMissingInterface
	}
	switch service := strings.ToLower(control.Driver.Service); service {
	case "winusb":
		return stateReady
	case "":
		return stateNoDriver
	default:
		return stateWrongDriver
	}
}

// open opens the control interface and starts reading its status stream.
func (s *Session) open(status usb.Status, model duml.Model) {
	control := status.Control
	if control == nil {
		return
	}
	conn, err := usb.Open(*control)
	if err != nil {
		s.logf(true, "打开接收器失败：%v", err)
		s.mu.Lock()
		s.snapshot.ConnectionError = "打开厂商接口失败：" + err.Error()
		s.mu.Unlock()
		s.changed()
		return
	}

	s.mu.Lock()
	s.conn = conn
	s.snapshot.Connected = true
	s.snapshot.ConnectionError = ""
	s.snapshot.State = duml.NewState()
	s.snapshot.Notes = conn.Notes()
	s.mu.Unlock()

	s.logf(false, "已连接接收器（%s，接口 %d，端点 0x%02x/0x%02x）",
		model.Name, model.Interface, model.BulkOut, model.BulkIn)
	for _, note := range conn.Notes() {
		s.logf(false, "  %s", note)
	}
	s.changed()
	go s.read(conn)
}

// close drops the connection and records why.
func (s *Session) close(why string) {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.snapshot.Connected = false
	s.snapshot.FramesPerSecond = 0
	s.snapshot.ConnectionError = why
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
	s.logf(false, "%s", why)
}

// read decodes the receiver's status stream until the connection fails.
func (s *Session) read(conn *usb.Conn) {
	buf := make([]byte, 1024)
	pending := make([]byte, 0, 2048)
	reported := 0
	for {
		n, err := conn.Read(buf)
		if err == usb.ErrTimeout {
			continue
		}
		if err != nil {
			s.mu.Lock()
			current := s.conn
			s.mu.Unlock()
			if current == conn {
				s.close("与接收器的连接中断：" + err.Error())
			}
			return
		}
		pending = append(pending, buf[:n]...)
		for {
			frame, rest := duml.TakeFrame(pending)
			if frame == nil {
				pending = append(pending[:0], rest...)
				break
			}
			pending = append(pending[:0], rest...)
			s.handleFrame(frame, &reported)
		}
		if len(pending) > 8192 {
			pending = pending[:0]
		}
	}
}

// handleFrame decodes one frame and wakes the UI.
func (s *Session) handleFrame(frame []byte, reported *int) {
	switch duml.FrameKindOf(frame) {
	case duml.KindAck:
		if seq, ok := duml.AckSeq(frame); ok {
			s.mu.Lock()
			ch, waiting := s.pending[seq]
			delete(s.pending, seq)
			s.mu.Unlock()
			if waiting {
				close(ch)
			}
		}
		return
	case duml.KindPush:
		// handled below
	default:
		return
	}

	s.mu.Lock()
	next, ok := duml.Decode(s.snapshot.State, frame)
	if ok {
		first := !s.snapshot.State.DialectKnown
		s.snapshot.State = next
		s.snapshot.LastFrame = next.Updated
		s.frames++
		if first {
			s.mu.Unlock()
			s.logf(false, "协议版本 %s，状态推送已开始", next.Dialect)
			s.changed()
			return
		}
		s.mu.Unlock()
		s.changed()
		return
	}
	s.mu.Unlock()

	// A frame the decoder does not know is worth showing once, since it may
	// mean a firmware that speaks a newer protocol.
	if *reported < 3 {
		*reported++
		s.logf(false, "未识别的数据帧（%d 字节）：% x", len(frame), frame)
	}
}

// Send writes a setting to the receiver and waits for its acknowledgement.
func (s *Session) Send(settingID, value string) error {
	return s.send(settingID, value, 0)
}

// SendToTransmitter writes a per-transmitter setting to one unit.
func (s *Session) SendToTransmitter(settingID, value string, unit int) error {
	if unit < 1 || unit > 2 {
		return fmt.Errorf("发射器编号 %d 不存在", unit)
	}
	return s.send(settingID, value, unit)
}

func (s *Session) send(settingID, value string, unit int) error {
	setting, known := duml.SettingByID(settingID)
	if !known {
		return fmt.Errorf("未知设置 %q", settingID)
	}

	s.mu.Lock()
	conn := s.conn
	state := s.snapshot.State
	if conn == nil {
		s.mu.Unlock()
		return errors.New("接收器未连接")
	}
	command, target, ok := setting.Command(state.Dialect)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%s 在当前固件（%s）上不可用", setting.Label, state.Dialect)
	}
	if unit >= 1 {
		target = duml.TargetUnit(unit)
	}
	wire, ok := setting.WireOf(value)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%s 没有取值 %q", setting.Label, value)
	}

	s.seq++
	seq := s.seq
	ch := make(chan struct{})
	s.pending[seq] = ch
	// Show the new value at once; the next status frame confirms or
	// corrects it.
	s.snapshot.State.Settings[settingID] = value
	s.mu.Unlock()

	frame := duml.BuildCommand(state.Dialect, seq, target, command, wire)
	if _, err := conn.Write(frame); err != nil {
		s.forget(seq)
		return fmt.Errorf("发送 %s 失败：%w", setting.Label, err)
	}

	timer := time.NewTimer(s.opts.AckTimeout)
	defer timer.Stop()
	select {
	case <-ch:
		s.logf(false, "已设置 %s = %s", setting.Label, valueLabel(setting, value))
		s.changed()
		return nil
	case <-timer.C:
		s.forget(seq)
		return fmt.Errorf("%s 没有在 %s 内确认", setting.Label, s.opts.AckTimeout)
	case <-s.stop:
		s.forget(seq)
		return errors.New("程序正在退出")
	}
}

func (s *Session) forget(seq uint16) {
	s.mu.Lock()
	delete(s.pending, seq)
	s.mu.Unlock()
}

// valueLabel renders a value slug with the label the UI shows.
func valueLabel(setting duml.Setting, value string) string {
	if opt, ok := setting.Option(value); ok {
		return opt.Label
	}
	return value
}

// Install installs the WinUSB driver on the control interface, prompting for
// administrator rights. progress receives the installer's output as it runs.
func (s *Session) Install(progress func(string)) error {
	s.mu.Lock()
	model := s.snapshot.Model
	haveModel := s.snapshot.HaveModel
	s.snapshot.Installing = true
	s.snapshot.InstallLog = nil
	s.mu.Unlock()
	s.changed()
	defer func() {
		s.mu.Lock()
		s.snapshot.Installing = false
		s.mu.Unlock()
		s.Rescan()
		s.changed()
	}()

	if !haveModel {
		err := errors.New("请先插上接收器：驱动要绑定的是它的厂商接口")
		s.logf(true, "%v", err)
		return err
	}

	emit := func(line string) {
		s.mu.Lock()
		s.snapshot.InstallLog = append(s.snapshot.InstallLog, line)
		s.mu.Unlock()
		s.changed()
		if progress != nil {
			progress(line)
		}
	}

	s.logf(false, "开始安装 WinUSB 驱动（%s，接口 %d）", model.Name, model.Interface)
	s.mu.Lock()
	logDir := s.dataDir
	s.mu.Unlock()

	// The routes that can work, best first. A package signed at build time
	// asks nothing of this machine; signing on the fly is the developer
	// route, which needs the Windows SDK and installs a self-signed
	// certificate into the machine's trust stores.
	var result usb.InstallResult
	var err error
	switch {
	case usb.HasEmbeddedPackage():
		result, err = usb.InstallEmbeddedPackage(logDir, emit)
	case usb.SignedInstallAvailable():
		s.logf(false, "这个版本没有内置已签名的驱动包，改用开发模式："+
			"在本机创建自签名证书、签好驱动包再安装（会改动系统信任，卸载时移除）")
		result, err = usb.InstallSignedDriver(model, logDir, emit)
	default:
		s.logf(true, "既没有内置签名包，也没有 Windows SDK 的 makecat/signtool："+
			"Windows 会拒绝未签名的驱动包，请改用 Zadig")
		result, err = usb.InstallDriver(model, logDir, emit)
	}
	s.recordInstall(result)
	for _, line := range result.Log {
		s.logf(false, "%s", line)
	}
	if err != nil {
		if errors.Is(err, usb.ErrCanceled) {
			s.logf(true, "已取消管理员授权，驱动未安装")
			return err
		}
		if hint := usb.UnsignedHint(result.Log); hint != "" {
			s.logf(true, "驱动安装失败：%s", hint)
			return errors.New("驱动安装被 Windows 拒绝（详见安装日志）")
		}
		s.logf(true, "驱动安装失败：%v", err)
		return err
	}
	s.logf(false, "驱动安装完成；如果接口还没出现，请重新插拔接收器")
	return nil
}

// recordInstall keeps what the installer wrote and, when it failed for want of
// a signature, says so once in a way the window can act on.
func (s *Session) recordInstall(result usb.InstallResult) {
	s.mu.Lock()
	s.snapshot.DriverLogPath = result.LogPath
	if hint := usb.UnsignedHint(result.Log); hint != "" {
		s.snapshot.UnsignedRefused = true
	}
	s.mu.Unlock()
}

// Uninstall removes the WinUSB package from the control interface.
func (s *Session) Uninstall(progress func(string)) error {
	s.mu.Lock()
	control := s.snapshot.Status.Control
	s.snapshot.Installing = true
	s.snapshot.InstallLog = nil
	s.mu.Unlock()
	s.changed()
	defer func() {
		s.mu.Lock()
		s.snapshot.Installing = false
		s.mu.Unlock()
		s.Rescan()
		s.changed()
	}()

	if control == nil {
		err := errors.New("厂商接口没有出现，也就没有可移除的驱动")
		s.logf(true, "%v", err)
		return err
	}

	// The interface must be released before its driver can be removed.
	s.close("卸载驱动前先断开接收器")

	s.mu.Lock()
	dataDir := s.dataDir
	s.mu.Unlock()

	emit := func(line string) {
		s.mu.Lock()
		s.snapshot.InstallLog = append(s.snapshot.InstallLog, line)
		s.mu.Unlock()
		s.changed()
		if progress != nil {
			progress(line)
		}
	}

	result, err := usb.UninstallDriver(*control, dataDir, emit)
	s.recordInstall(result)
	for _, line := range result.Log {
		s.logf(false, "%s", line)
	}
	if err != nil {
		s.logf(true, "卸载驱动失败：%v", err)
		return err
	}
	s.logf(false, "驱动已移除；请重新插拔接收器，让 Windows 回到默认驱动")
	return nil
}

// OpenZadig starts libwdi's graphical installer, which the user may already
// have, with the receiver selected as far as the command line allows.
func (s *Session) OpenZadig() error {
	path, ok := usb.FindZadig()
	if !ok {
		return errors.New("没有找到 Zadig：请先下载 zadig.exe，或用「选择…」指定位置")
	}
	if err := usb.StartDetached(path); err != nil {
		return err
	}
	s.logf(false, "已打开 Zadig：%s", path)
	return nil
}

// OpenWdiSimple runs a user-supplied wdi-simple.exe elevated, with the
// command line this project's notes document.
func (s *Session) OpenWdiSimple(path string) error {
	if path == "" {
		found, ok := usb.FindWdiSimple()
		if !ok {
			return errors.New("没有找到 wdi-simple.exe：请用「选择…」指定位置")
		}
		path = found
	}
	s.mu.Lock()
	model := s.snapshot.Model
	haveModel := s.snapshot.HaveModel
	s.mu.Unlock()
	if !haveModel {
		return errors.New("请先插上接收器")
	}

	s.mu.Lock()
	s.snapshot.Installing = true
	s.snapshot.InstallLog = nil
	dataDir := s.dataDir
	s.mu.Unlock()
	s.changed()
	defer func() {
		s.mu.Lock()
		s.snapshot.Installing = false
		s.mu.Unlock()
		s.Rescan()
		s.changed()
	}()

	emit := func(line string) {
		s.mu.Lock()
		s.snapshot.InstallLog = append(s.snapshot.InstallLog, line)
		s.mu.Unlock()
		s.changed()
	}
	result, err := usb.InstallWithWdiSimple(model, path, dataDir, emit)
	s.recordInstall(result)
	for _, line := range result.Log {
		s.logf(false, "%s", line)
	}
	if err != nil {
		s.logf(true, "wdi-simple.exe 安装失败：%v", err)
		return err
	}
	s.logf(false, "wdi-simple.exe 安装完成；请重新插拔接收器")
	return nil
}

// RememberWdiSimple stores the path of a helper the user picked.
func (s *Session) RememberWdiSimple(path string) {
	s.mu.Lock()
	s.tools.WdiSimple, s.tools.WdiSimpleSet = path, path != ""
	s.snapshot.Tools = s.tools
	configPath := s.configPath
	s.mu.Unlock()
	if configPath != "" {
		_ = saveConfig(configPath, config{WdiSimple: path})
	}
	s.changed()
}

// refreshTools looks for the helper programs, using the remembered path for
// wdi-simple.exe when there is one.
func (s *Session) refreshTools() {
	tools := Tools{
		Elevated:      usb.IsElevated(),
		SDKTools:      usb.SignedInstallAvailable(),
		SignedPackage: usb.HasEmbeddedPackage(),
	}
	s.mu.Lock()
	configPath := s.configPath
	s.mu.Unlock()

	if configPath != "" {
		if cfg, err := loadConfig(configPath); err == nil && cfg.WdiSimple != "" {
			tools.WdiSimple, tools.WdiSimpleSet = cfg.WdiSimple, true
		}
	}
	if !tools.WdiSimpleSet {
		if path, ok := usb.FindWdiSimple(); ok {
			tools.WdiSimple, tools.WdiSimpleSet = path, true
		}
	}
	if path, ok := usb.FindZadig(); ok {
		tools.Zadig, tools.ZadigSet = path, true
	}

	s.mu.Lock()
	s.tools = tools
	s.snapshot.Tools = tools
	s.mu.Unlock()
	s.changed()
}

// Diagnostics renders everything the app knows about the device for copying
// into a bug report or an issue.
func (s *Session) Diagnostics() string {
	snap := s.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "DJI Mic 接收器控制台 · 诊断信息\n")
	fmt.Fprintf(&b, "时间：%s\n", snap.Time.Format("2006-01-02 15:04:05"))
	if snap.HaveModel {
		fmt.Fprintf(&b, "型号表：%s（接口 %d，端点 0x%02x/0x%02x）\n",
			snap.Model.Name, snap.Model.Interface, snap.Model.BulkOut, snap.Model.BulkIn)
	} else {
		fmt.Fprintf(&b, "型号表：没有匹配的型号\n")
	}
	fmt.Fprintf(&b, "已连接：%v  协议：%s  推送 %.1f 帧/秒\n",
		snap.Connected, dialectText(snap), snap.FramesPerSecond)
	if snap.ConnectionError != "" {
		fmt.Fprintf(&b, "状态：%s\n", snap.ConnectionError)
	}
	fmt.Fprintf(&b, "\n设备节点：\n")
	for _, d := range snap.Status.Devices {
		present := "缺失"
		if d.Present {
			present = "在线"
		}
		fmt.Fprintf(&b, "  %s  %s\n", d.InstanceID, present)
		fmt.Fprintf(&b, "    %s；服务=%s；驱动包=%s；提供者=%s\n",
			blankText(d.Description), blankText(d.Driver.Service),
			blankText(d.Driver.InfPath), blankText(d.Driver.Provider))
	}
	if len(snap.Notes) > 0 {
		fmt.Fprintf(&b, "\n打开设备时的记录：\n")
		for _, n := range snap.Notes {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	if snap.State.DialectKnown {
		fmt.Fprintf(&b, "\n接收器：%s  序列号 %s  固件 %s\n",
			blankText(snap.State.RX.Name), blankText(snap.State.RX.Serial), blankText(snap.State.RX.Firmware))
		for i, tx := range snap.State.TX {
			state := "未连接"
			if tx.Present {
				state = fmt.Sprintf("已连接 序列号 %s 固件 %s 电平 %d",
					blankText(tx.Serial), blankText(tx.Firmware), tx.Level)
			}
			fmt.Fprintf(&b, "  发射器 %d：%s\n", i+1, state)
		}
		settings := make([]string, 0, len(snap.State.Settings))
		for id, value := range snap.State.Settings {
			settings = append(settings, id+"="+value)
		}
		sort.Strings(settings)
		fmt.Fprintf(&b, "  设置：%s\n", strings.Join(settings, " "))
	}
	fmt.Fprintf(&b, "\n工具：wdi-simple=%s  zadig=%s  管理员=%v\n",
		blankText(snap.Tools.WdiSimple), blankText(snap.Tools.Zadig), snap.Tools.Elevated)
	fmt.Fprintf(&b, "\n活动日志：\n")
	for _, line := range snap.Log {
		fmt.Fprintf(&b, "  %s  %s\n", line.At.Format("15:04:05"), line.Text)
	}
	return b.String()
}

func dialectText(snap Snapshot) string {
	if !snap.State.DialectKnown {
		return "未知"
	}
	return snap.State.Dialect.String()
}

func blankText(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
