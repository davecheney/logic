package logic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type testPort struct {
	input bytes.Buffer
	wrote strings.Builder
	reply func(string) string
	chunk int
	short bool
}

func (p *testPort) Read(b []byte) (int, error) {
	if p.chunk > 0 {
		b = b[:min(len(b), p.chunk)]
	}
	if p.input.Len() == 0 {
		return 0, nil
	}
	return p.input.Read(b)
}

func (p *testPort) Write(b []byte) (int, error) {
	p.wrote.Write(b)
	if p.short {
		return 0, nil
	}
	if p.reply != nil {
		p.input.WriteString(p.reply(string(b)))
	}
	return len(b), nil
}

func TestVM(t *testing.T) {
	p := &testPort{chunk: 1, reply: func(cmd string) string {
		switch cmd {
		case "!":
			return "old data!"
		case "?":
			return "?\rBS000500\r"
		case "7c@p":
			return "7c@p\rc0\r"
		default:
			return cmd
		}
	}}
	s, err := NewBitScope(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "BS000500" {
		t.Fatalf("ID %q", s.ID)
	}
	value, err := s.vm.register(regKitchenSinkB)
	if err != nil || value != 0xc0 {
		t.Fatalf("register = %#x, %v", value, err)
	}
	if err := s.vm.issue("21@12s"); err != nil {
		t.Fatal(err)
	}
}

func TestCancelTrace(t *testing.T) {
	for _, chunk := range []int{1, 3, 4096} {
		p := &testPort{chunk: chunk, reply: func(cmd string) string {
			if cmd == "K?" {
				return "00001234\r03\r00005678\r00000400\rK?\rBS000500\r"
			}
			return cmd
		}}
		v := vm{port: p, pending: []byte("02\r"), timeout: time.Second}
		if err := v.cancelTrace("BS000500"); err != nil {
			t.Fatal(err)
		}
		if err := v.issue("46@01s"); err != nil {
			t.Fatalf("command after cancellation, chunk %d: %v", chunk, err)
		}
		if strings.Contains(p.wrote.String(), "!") {
			t.Fatal("trace cancellation reset the generator")
		}
	}
}

func TestDelayedCommands(t *testing.T) {
	var commands []string
	p := &testPort{reply: func(cmd string) string {
		commands = append(commands, cmd)
		if strings.ContainsAny(cmd[:len(cmd)-1], ">UXYZ") {
			t.Fatalf("command sent before delayed acknowledgement: %q", cmd)
		}
		return cmd
	}}
	v := vm{port: p, timeout: time.Second}
	if err := v.issue("21@00s>UD"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(commands, "|"); got != "21@00s>|U|D" {
		t.Fatalf("command boundaries: %s", got)
	}
}

func TestVMErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		port  *testPort
		check error
	}{
		{"short write", &testPort{short: true}, io.ErrShortWrite},
		{"timeout", &testPort{}, errTimeout},
		{"bad echo", &testPort{reply: func(string) string { return "x" }}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := vm{port: tc.port, timeout: time.Millisecond}
			err := v.issue("?")
			if err == nil || tc.check != nil && !errors.Is(err, tc.check) {
				t.Fatalf("issue error = %v", err)
			}
		})
	}
}

func TestUnsupportedDevice(t *testing.T) {
	p := &testPort{reply: func(cmd string) string {
		if cmd == "?" {
			return "?\rBS999999\r"
		}
		return cmd
	}}
	if _, err := NewBitScope(p); err == nil {
		t.Fatal("accepted an unsupported device")
	}
}

func TestDump(t *testing.T) {
	for _, tc := range []struct {
		size int
		data string
		want []float64
	}{
		{1, "\x00\x80\xff", []float64{-1, 0, 1 - 1.0/128}},
		{2, "\x80\x00\x00\x00\x7f\xff", []float64{-1, 0, 1 - 1.0/32768}},
	} {
		p := &testPort{chunk: 1, reply: func(cmd string) string {
			if cmd == "A" {
				return cmd + tc.data
			}
			return cmd
		}}
		s := BitScope{vm: vm{port: p, timeout: time.Second}}
		got, err := s.dump(0, 0, 3, tc.size, -1, 1)
		if err != nil {
			t.Fatal(err)
		}

		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("size %d: sample %d = %v, want %v", tc.size, i, got[i], tc.want[i])
			}
		}
	}
}

func TestCaptureKeepsGeneratorState(t *testing.T) {
	for _, sinkB := range []string{"00", "40", "80", "c0"} {
		t.Run(sinkB, func(t *testing.T) {
			p := &testPort{reply: func(cmd string) string {
				switch {
				case cmd == "7c@p":
					return cmd + "\r" + sinkB + "\r"
				case cmd == "D":
					return cmd + "01\r00000001\r00000400\r"
				case cmd == "A":
					return cmd + strings.Repeat("\x00", 2000)
				default:
					return cmd
				}
			}}
			s := &BitScope{vm: vm{port: p, timeout: time.Second}}
			tr, err := s.Capture(context.Background(), ScopeConfig{
				A: true, Duration: time.Millisecond, Samples: 1000,
				Low: -1, High: 1,
				Trigger: Trigger{Position: 0.25, Timeout: time.Millisecond},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := "7c@80s"
			if sinkB == "40" || sinkB == "c0" {
				want = "7c@c0s"
			}
			if !strings.Contains(p.wrote.String(), want) {
				t.Fatalf("capture did not preserve generator state: %s", p.wrote.String())
			}
			if len(tr.A) != 1000 || tr.Rate != 1e6 || tr.TriggerAt != 250 {
				t.Fatalf("unexpected trace: samples=%d rate=%v trigger=%d", len(tr.A), tr.Rate, tr.TriggerAt)
			}
		})
	}
}
