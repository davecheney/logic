package bitscope

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/davecheney/logic"
)

func TestAWGTiming(t *testing.T) {
	for _, frequency := range []float64{10, 13.7, 137, 1000, 9999, 10000} {
		ticks, size, actual, err := awgTiming(frequency)
		if err != nil || ticks < 40 || ticks > 65535 || size < 50 || size > 1024 ||
			math.Abs(actual-frequency)/frequency > 0.001 {
			t.Fatalf("%v Hz: ticks %v size %v actual %v error %v", frequency, ticks, size, actual, err)
		}
	}
	for _, frequency := range []float64{0, 9, 10001, math.NaN(), math.Inf(1)} {
		if _, _, _, err := awgTiming(frequency); err == nil {
			t.Fatalf("accepted %v Hz", frequency)
		}
	}
}

func TestWaveforms(t *testing.T) {
	for _, w := range []logic.Waveform{logic.Sine, logic.Square, logic.Triangle, logic.Saw} {
		lo, hi := 1.0, 0.0
		for i := range 1024 {
			v, err := waveValue(w, float64(i)/1024)
			if err != nil || v < 0 || v > 1 {
				t.Fatalf("%s sample %d: %v %v", w, i, v, err)
			}
			lo, hi = min(lo, v), max(hi, v)
		}
		if lo != 0 || hi < 0.99 {
			t.Fatalf("%s range %v..%v", w, lo, hi)
		}
	}
	if _, err := waveValue("invalid", 0); err == nil {
		t.Fatal("accepted invalid waveform")
	}
}

func TestAWGEnableCycles(t *testing.T) {
	p := &testPort{chunk: 1, reply: func(cmd string) string {
		switch cmd {
		case "7c@p":
			return cmd + "\rc0\r"
		default:
			return cmd
		}
	}}
	s := &BitScope{name: "BS000500", vm: vm{port: p, timeout: time.Second}}
	for _, enabled := range []bool{false, true, false, true} {
		if _, err := s.SetAWG(AWGConfig{Enabled: enabled, Frequency: 1000, Waveform: logic.Sine}); err != nil {
			t.Fatalf("enabled=%v: %v", enabled, err)
		}
	}
}

func TestSine1kHz(t *testing.T) {
	var commands []string
	p := &testPort{reply: func(cmd string) string {
		commands = append(commands, cmd)
		if cmd == "7c@p" {
			return cmd + "\r80\r"
		}
		return cmd
	}}
	s := &BitScope{vm: vm{port: p, timeout: time.Second}}
	if err := s.StartSine1kHz(); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 5 {
		t.Fatalf("commands: %q", commands)
	}
	for i, want := range []string{
		"46@00s47@00s5a@00s5b@80s5c@00s5d@00sY",
		"7c@p",
		"46@00s47@00s54@93s55@4ds56@cas57@a6s5a@25s5b@06s5c@01s5d@00s4c@00s4d@00s4e@00s4f@00s4a@e8s4b@03sX",
		"46@02s47@00s50@28s51@00s52@e8s53@03s5e@0as5f@00s60@01s61@00s78@00s79@7fs48@04s49@80sZ",
		"7c@c0sU",
	} {
		if commands[i] != want {
			t.Errorf("command %d = %q, want %q", i, commands[i], want)
		}
	}
	commands = nil
	if err := s.StopAWG(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(commands, "|"); got != "7c@p|46@01s47@00sZ|7c@80sU" {
		t.Fatalf("stop commands: %s", got)
	}
}
