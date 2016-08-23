package main

import (
	"bufio"
	"fmt"
	"github.com/abates/hdhomerun"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

var (
	transcoderArgs = [][]string{
		[]string{
			"720p",
			"-b:v", "1500k",
			"-maxrate", "1500k",
			"-bufsize", "1000k",
			"-vf", "scale=-1:720",
		},
		[]string{
			"540",
			"-b:v", "800k",
			"-maxrate", "800k",
			"-bufsize", "500k",
			"-vf", "scale=-1:540",
		},
		[]string{
			"360",
			"-b:v", "400k",
			"-maxrate", "400k",
			"-bufsize", "400k",
			"-vf", "scale=-1:360",
		},
	}
)

func usage(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, "%s\n", msg)
	}
	fmt.Printf("Usage: %s <device IP>:<device port> <frequency> <program>\n", os.Args[0])
	os.Exit(-1)
}

type transcoder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser
	wg     sync.WaitGroup
}

func newTranscoder(prefix string, args ...string) (*transcoder, error) {
	t := &transcoder{}

	localArgs := []string{
		"-y",
		"-i", "pipe:",
		"-c:a", "copy",
		"-c:v", "libx264",
		"-x264opts", "keyint=24:min-keyint=24:no-scenecut",
	}
	localArgs = append(localArgs, args...)
	localArgs = append(localArgs,
		"-f", "segment",
		"-segment_time", "5",
		fmt.Sprintf("output/%s-%%04d.mp4", prefix),
	)

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}

	t.cmd = exec.Command(ffmpeg, localArgs...)
	t.stdin, _ = t.cmd.StdinPipe()
	t.stderr, _ = t.cmd.StderrPipe()
	err = t.cmd.Start()
	if err == nil {
		t.wg.Add(1)
		go func() {
			var line []byte
			var isPrefix bool
			var err error

			longLine := ""
			reader := bufio.NewReader(t.stderr)
			for err == nil {
				line, isPrefix, err = reader.ReadLine()
				if isPrefix {
					longLine += string(line)
				} else {
					if longLine != "" {
						fmt.Fprintf(os.Stderr, "%s %s%s\n", prefix, longLine, string(line))
						longLine = ""
					} else {
						fmt.Fprintf(os.Stderr, "%s %s\n", prefix, string(line))
					}
				}
			}

			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			t.wg.Done()
		}()
	}
	return t, err
}

func (t *transcoder) Write(p []byte) (int, error) {
	return t.stdin.Write(p)
}

func (t *transcoder) Close() error {
	err := t.stdin.Close()
	t.wg.Wait()
	return err
}

func localAddr(deviceIP net.IP) (net.IP, error) {
	for _, i := range assertFail(net.Interfaces()).([]net.Interface) {
		for _, ip := range assertFail(i.Addrs()).([]net.Addr) {
			if network, isNet := ip.(*net.IPNet); isNet && network.Contains(deviceIP) {
				return network.IP, nil
			}
		}
	}
	return nil, fmt.Errorf("No interface is on the same network as %s", deviceIP.String())
}

func assertUsage(i interface{}, err error) interface{} {
	if err != nil {
		usage(err.Error())
	}
	return i
}

func assertFail(i interface{}, err error) interface{} {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Connection Error: %v", err)
		os.Exit(-1)
	}
	return i
}

func main() {
	if len(os.Args) < 4 {
		usage("")
	}

	tcpAddr := assertUsage(net.ResolveTCPAddr("tcp", os.Args[1])).(*net.TCPAddr)
	frequency := assertUsage(strconv.ParseInt(os.Args[2], 10, 0)).(int64)
	program := assertUsage(strconv.ParseInt(os.Args[3], 10, 0)).(int64)
	lip := assertUsage(localAddr(tcpAddr.IP)).(net.IP)

	device := assertFail(hdhomerun.ConnectTCP(tcpAddr)).(hdhomerun.Device)
	if closer, closeable := device.(io.Closer); closeable {
		defer closer.Close()
	}

	conn := assertFail(net.ListenUDP("udp", &net.UDPAddr{lip, 0, ""})).(*net.UDPConn)

	transcoders := []*transcoder{}
	for _, args := range transcoderArgs {
		t, err := newTranscoder(args[0], args[1:]...)
		if err == nil {
			transcoders = append(transcoders, t)
		} else {
			fmt.Fprintf(os.Stderr, "Failed to create transcoder: %v", err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		buffer := make([]byte, 1500)
		for {
			n, err := conn.Read(buffer)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v", err)
			} else {
				for _, t := range transcoders {
					t.Write(buffer[0:n])
				}
			}
		}
	}()

	tuner := device.Tuner(0)

	err := tuner.Stream(int(frequency), int(program), conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to tune: %v\n", err)
		os.Exit(-1)
	}
	wg.Wait()
}
