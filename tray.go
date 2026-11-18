package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"fyne.io/systray"
)

const maxAccounts = 16

var thresholds = []float64{90, 94, 96, 98}

type tray struct {
	s         *switcher
	status    *systray.MenuItem
	update    *systray.MenuItem
	accounts  [maxAccounts]*systray.MenuItem
	add       *systray.MenuItem
	autostart *systray.MenuItem
	lastIcon  string
	updates   chan struct{}
}

func (t *tray) onReady() {
	systray.SetTooltip(appName())
	t.setIcon(-1, defaultThreshold)

	t.status = systray.AddMenuItem("", "")
	t.status.Disable()
	t.status.Hide()
	t.update = systray.AddMenuItem("", tr("Download the new version and restart on it", "下载新版本并以新版本重启"))
	t.update.Hide()
	for i := range t.accounts {
		t.accounts[i] = systray.AddMenuItemCheckbox("", tr("Switch the Claude app to this account", "将 Claude 切换到此账号"), false)
		t.accounts[i].Hide()
	}
	systray.AddSeparator()
	t.add = systray.AddMenuItem(tr("Add account…", "添加账号…"),
		tr("Restart Claude on its sign-in screen, your current account is kept", "重启 Claude 并打开登录页面，当前账号会保留"))
	systray.AddSeparator()
	// Rewriting an enabled entry keeps its path current and adds the Task
	// Manager record that entries from before v0.1.2 are missing.
	if autostartEnabled() {
		if err := setAutostart(true); err != nil {
			log.Printf("refresh start at login: %v", err)
		}
	}
	t.autostart = systray.AddMenuItemCheckbox(tr("Start at login", "开机时启动"), "", autostartEnabled())
	quit := systray.AddMenuItem(tr("Quit", "退出"), "")

	for i, item := range t.accounts {
		go func() {
			for range item.ClickedCh {
				snap := t.s.snapshot()
				if i < len(snap.Accounts) && snap.Accounts[i].UUID != snap.Active {
					go t.s.run(tr("Switch", "切换"), func() error { return t.s.switchTo(snap.Accounts[i].UUID, byHand) })
				}
				t.s.onChange()
			}
		}()
	}
	go func() {
		for {
			select {
			case <-t.add.ClickedCh:
				go t.s.run(tr("Add account", "添加账号"), t.s.addAccount)
			case <-t.autostart.ClickedCh:
				if err := setAutostart(!t.autostart.Checked()); err != nil {
					t.s.setStatus(tr("Start at login: ", "开机时启动：") + err.Error())
				}
				setChecked(t.autostart, autostartEnabled())
			case <-t.update.ClickedCh:
				if v := t.s.snapshot().Update; v != "" {
					go t.s.installUpdate(context.Background(), v)
				}
			case <-quit.ClickedCh:
				quit.Disable()
				go func() {
					t.s.drain()
					systray.Quit()
				}()
				return
			}
		}
	}()

	// Menu updates arrive from worker goroutines; one goroutine applies them.
	t.s.onChange = func() {
		select {
		case t.updates <- struct{}{}:
		default:
		}
	}
	go func() {
		for range t.updates {
			t.render()
		}
	}()
	t.s.onChange()
	go t.loop()
	go t.updateLoop()
}

// loop runs refresh every few seconds while a new sign-in is expected,
// otherwise every minute; refresh decides which accounts are due.
func (t *tray) loop() {
	var last time.Time
	for ; ; time.Sleep(5 * time.Second) {
		if !t.s.adding() && time.Since(last) < hotEvery {
			continue
		}
		last = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		t.s.refresh(ctx)
		cancel()
	}
}

// updateLoop looks for a new release a minute after start, then every hour;
// checkUpdate decides whether a day has passed since the last answer.
func (t *tray) updateLoop() {
	time.Sleep(time.Minute)
	for {
		t.s.checkUpdate(context.Background())
		time.Sleep(time.Hour)
	}
}

func (t *tray) render() {
	snap := t.s.snapshot()
	now := time.Now()

	switch {
	case snap.Status != "":
		t.status.SetTitle(snap.Status)
		t.status.Show()
	case snap.Note != "":
		t.status.SetTitle(snap.Note)
		t.status.Show()
	default:
		t.status.Hide()
	}
	if snap.Update != "" {
		t.update.SetTitle(fmt.Sprintf(tr("Version %s is out, click to update", "新版本 %s 已发布，点击更新"), snap.Update))
		t.update.Show()
	} else {
		t.update.Hide()
	}
	if len(snap.Accounts) == 0 && snap.Status == "" {
		t.status.SetTitle(tr("Open Claude and sign in", "请打开 Claude 并登录"))
		t.status.Show()
	}

	if len(snap.Accounts) > maxAccounts {
		log.Printf("showing %d of %d saved accounts (tray menu limit %d)", maxAccounts, len(snap.Accounts), maxAccounts)
	}
	activePct := -1.0
	tip := appName()
	for i, item := range t.accounts {
		if i >= len(snap.Accounts) {
			item.Hide()
			continue
		}
		a := snap.Accounts[i]
		detail := usageText(a, snap.Threshold, now)
		item.SetTitle(a.label() + menuTab + detail)
		setChecked(item, a.UUID == snap.Active)
		if snap.Busy {
			item.Disable()
		} else {
			item.Enable()
		}
		item.Show()
		if a.UUID == snap.Active {
			tip = a.label() + "\n" + detail
			if a.Usage != nil {
				activePct = a.Usage.current(now).Session
			}
		}
	}
	systray.SetTooltip(tip)
	t.setIcon(activePct, snap.Threshold)

	if snap.Busy {
		t.add.Disable()
	} else {
		t.add.Enable()
	}
}

func appName() string { return tr("Claude Switch", "Claude 切换") }

// menuTab right-aligns the usage on Windows, where a tab in a menu title
// starts a right-aligned column.
var menuTab = map[bool]string{true: "\t", false: "    "}[runtime.GOOS == "windows"]

func usageText(a Account, threshold float64, now time.Time) string {
	if a.Problem == signedOut {
		return tr(signedOut, "登录已失效，请重新添加")
	}
	if a.Problem != "" {
		return a.Problem
	}
	if a.Usage == nil {
		return tr("usage unknown", "用量未知")
	}
	u := a.Usage.current(now)
	parts := []string{fmt.Sprintf(tr("5h %.0f%%", "5小时 %.0f%%"), u.Session), fmt.Sprintf(tr("week %.0f%%", "本周 %.0f%%"), u.Weekly)}
	parts[0] += resetPart(u.Session, u.SessionReset, threshold, now)
	// The weekly reset is always shown: it decides how much of the week is left to use.
	parts[1] += resetPart(u.Weekly, u.WeeklyReset, threshold, now)
	return strings.Join(parts, " · ")
}

// resetPart says when a limit resets: "until" once it is at the threshold,
// so it reads as blocked until then.
func resetPart(pct float64, at time.Time, threshold float64, now time.Time) string {
	switch {
	case at.IsZero() || !at.After(now):
		return ""
	case pct >= threshold:
		return fmt.Sprintf(tr(" until %s", "，%s 才重置"), resetText(at, now))
	}
	return fmt.Sprintf(tr(", resets %s", "，%s 重置"), resetText(at, now))
}

var weekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

func resetText(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	switch {
	case at.YearDay() == now.YearDay() && at.Year() == now.Year():
		return at.Format("15:04")
	case at.Sub(now) < 6*24*time.Hour:
		return tr(at.Format("Mon 15:04"), weekdays[at.Weekday()]+" "+at.Format("15:04"))
	}
	// A weekday alone would read as today for a reset a week out.
	return tr(at.Format("Mon 2 Jan 15:04"), at.Format("1月2日 ")+weekdays[at.Weekday()]+" "+at.Format("15:04"))
}

func (t *tray) setIcon(pct, threshold float64) {
	key := fmt.Sprintf("%.0f/%.0f", pct, threshold)
	if key == t.lastIcon {
		return
	}
	t.lastIcon = key
	systray.SetIcon(trayIcon(pct, threshold))
}

func setChecked(item *systray.MenuItem, on bool) {
	if on != item.Checked() {
		if on {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}
