// Package session keeps one receiver under watch: it finds the device,
// opens its control interface, decodes the status stream it pushes, and sends
// settings back. The window only ever reads the snapshot this package keeps,
// so a device that appears, disappears or reboots never blocks the UI.
package session

import (
	"errors"
	"fmt"
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
	// AckTimeout bounds the wait for the device to report the requested value.
	AckTimeout time.Duration
	// OnChange is called whenever the snapshot changes. It may be called
	// from any goroutine and should return quickly.
	OnChange func()
}

// LogLine is one entry of the session's activity log.
type LogLine struct {
	At   time.Time
	Text string
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

	// Log is the recent activity log, newest last.
	Log []LogLine
	// Installing is true while a driver install or removal runs.
	Installing bool
	// InstallLog is the output of the running or last install.
	InstallLog []string
}

// Session watches one receiver.
type Session struct {
	opts Options

	mu       sync.Mutex
	snapshot Snapshot
	conn     connection
	seq      uint16
	pending  map[uint16]*pendingSetting
	frames   int
	rate     float64
	stop     chan struct{}
	stopped  bool

	// lastKind is the device-tree state the previous scan found, so a change
	// can be logged once rather than on every tick.
	lastKind  stateKind
	kindKnown bool
}

type pendingSetting struct {
	id           string
	value        string
	unit         int
	acknowledged bool
	done         chan error
}

type connection interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
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
		pending: map[uint16]*pendingSetting{},
		stop:    make(chan struct{}),
	}
	s.snapshot = Snapshot{
		Time:  time.Now(),
		State: duml.NewState(),
	}
	return s
}

// SetOnChange sets the callback that runs when the snapshot changes. It is
// set after the window exists, since it redraws it.
func (s *Session) SetOnChange(fn func()) {
	s.mu.Lock()
	s.opts.OnChange = fn
	s.mu.Unlock()
}

// Start begins watching the device in the background.
func (s *Session) Start() {
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
	snap.State = s.snapshot.State.Clone()
	snap.Log = append([]LogLine(nil), s.snapshot.Log...)
	snap.InstallLog = append([]string(nil), s.snapshot.InstallLog...)
	return snap
}

// Rescan asks for an immediate scan of the device tree.
func (s *Session) Rescan() {
	go s.tick()
}

// Log appends a line to the activity log, which the diagnostics report
// includes.
func (s *Session) Log(format string, a ...any) {
	line := LogLine{At: time.Now(), Text: fmt.Sprintf(format, a...)}
	s.mu.Lock()
	log := append(s.snapshot.Log, line)
	if len(log) > 200 {
		log = log[len(log)-200:]
	}
	s.snapshot.Log = log
	s.mu.Unlock()
	s.changed()
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
			s.rate = float64(now - last)
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
		s.Log("%s", transition(kind))
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
		s.Log("打开接收器失败：%v", err)
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
	s.mu.Unlock()

	s.Log("已连接接收器（%s，接口 %d，端点 0x%02x/0x%02x）",
		model.Name, model.Interface, model.BulkOut, model.BulkIn)
	for _, note := range conn.Notes() {
		s.Log("  %s", note)
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
	for seq, pending := range s.pending {
		pending.done <- errors.New(why)
		delete(s.pending, seq)
	}
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
	s.Log("%s", why)
}

// read decodes the receiver's status stream until the connection fails.
func (s *Session) read(conn connection) {
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
			if pending := s.pending[seq]; pending != nil {
				pending.acknowledged = true
			}
			s.mu.Unlock()
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
		for seq, pending := range s.pending {
			value := next.Setting(pending.id)
			if pending.id == "voice-tone" && pending.unit > 0 {
				value = next.TX[pending.unit-1].VoiceTone
			}
			if value == pending.value {
				pending.done <- nil
				delete(s.pending, seq)
			}
		}
		s.snapshot.LastFrame = next.Updated
		s.frames++
		if first {
			s.mu.Unlock()
			s.Log("协议版本 %s，状态推送已开始", next.Dialect)
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
		s.Log("未识别的数据帧（%d 字节）：% x", len(frame), frame)
	}
}

// Send waits until the device reports the requested setting value.
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
	pending := &pendingSetting{id: settingID, value: value, unit: unit, done: make(chan error, 1)}
	s.pending[seq] = pending
	s.mu.Unlock()

	frame := duml.BuildCommand(state.Dialect, seq, target, command, wire)
	if _, err := conn.Write(frame); err != nil {
		s.forget(seq)
		return fmt.Errorf("发送 %s 失败：%w", setting.Label, err)
	}

	timer := time.NewTimer(s.opts.AckTimeout)
	defer timer.Stop()
	select {
	case err := <-pending.done:
		if err != nil {
			return err
		}
		s.Log("已设置 %s = %s", setting.Label, valueLabel(setting, value))
		s.changed()
		return nil
	case <-timer.C:
		s.mu.Lock()
		select {
		case err := <-pending.done:
			s.mu.Unlock()
			return err
		default:
		}
		acknowledged := pending.acknowledged
		delete(s.pending, seq)
		s.mu.Unlock()
		if acknowledged {
			return fmt.Errorf("%s：设备已应答，但未在 %s 内回报目标值，请检查设备状态", setting.Label, s.opts.AckTimeout)
		}
		return fmt.Errorf("%s 没有在 %s 内确认，请检查设备连接和当前状态", setting.Label, s.opts.AckTimeout)
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

// Install installs the build's signed driver package on the control
// interface, prompting for administrator rights. progress receives the
// installer's output as it runs.
func (s *Session) Install(progress func(string)) error {
	s.mu.Lock()
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

	emit := func(line string) {
		s.mu.Lock()
		s.snapshot.InstallLog = append(s.snapshot.InstallLog, line)
		s.mu.Unlock()
		s.changed()
		if progress != nil {
			progress(line)
		}
	}

	s.Log("开始安装 WinUSB 驱动")
	result, err := usb.InstallEmbeddedPackage("", emit)
	for _, line := range result.Log {
		s.Log("%s", line)
	}
	if err != nil {
		if errors.Is(err, usb.ErrCanceled) {
			s.Log("已取消管理员授权，驱动未安装")
			return err
		}
		s.Log("驱动安装失败：%v", err)
		return err
	}
	s.Log("驱动安装完成；如果接口还没出现，请重新插拔接收器")
	return nil
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
		s.Log("%v", err)
		return err
	}

	// The interface must be released before its driver can be removed.
	s.close("卸载驱动前先断开接收器")

	emit := func(line string) {
		s.mu.Lock()
		s.snapshot.InstallLog = append(s.snapshot.InstallLog, line)
		s.mu.Unlock()
		s.changed()
		if progress != nil {
			progress(line)
		}
	}

	result, err := usb.UninstallDriver(*control, "", emit)
	for _, line := range result.Log {
		s.Log("%s", line)
	}
	if err != nil {
		s.Log("卸载驱动失败：%v", err)
		return err
	}
	s.Log("驱动已移除；请重新插拔接收器，让 Windows 回到默认驱动")
	return nil
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
