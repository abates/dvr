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
	"strings"
	"sync"
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

	fmt.Fprintf(os.Stderr, "Command Args: %s\n", strings.Join(localArgs, " "))

	t.cmd = exec.Command("/usr/local/bin/ffmpeg", localArgs...)
	t.stdin, _ = t.cmd.StdinPipe()
	t.stderr, _ = t.cmd.StderrPipe()
	err := t.cmd.Start()
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

func main() {
	if len(os.Args) < 4 {
		usage("")
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", os.Args[1])
	if err != nil {
		usage(fmt.Sprintf("Invalid Address: %v", err))
	}

	frequency, err := strconv.ParseInt(os.Args[2], 10, 0)
	if err != nil {
		usage(fmt.Sprintf("Failed to parse frequency: %v", err))
	}

	program, err := strconv.ParseInt(os.Args[3], 10, 0)
	if err != nil {
		usage(fmt.Sprintf("Failed to parse program: %v", err))
	}

	device, err := hdhomerun.ConnectTCP(tcpAddr)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Connection Error: %v", err)
		os.Exit(-1)
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{net.IP{172, 16, 6, 123}, 0, ""})
	if err != nil {
		fmt.Fprintf(os.Stderr, "UDP Connection Error: %v", err)
		os.Exit(-1)
	}

	transcoderArgs := [][]string{
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
	err = tuner.Stream(int(frequency), int(program), conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to tune: %v\n", err)
		os.Exit(-1)
	}
	wg.Wait()
}

/*var wg sync.WaitGroup
wg.Add(1)
go func() {
	cmd := exec.Command("/usr/local/bin/ffmpeg",
		"-i", "pipe:",
		"-acodec", "copy",
		"-vcodec", "libx264",
		"-force_key_frames", "expr:gte(t,n_forced*5)",
		"-f", "segment",
		"-segment_time", "5",
		`output/output-%03d.mp4`,
	)
	stdinPipe, _ := cmd.StdinPipe()
	stderrPipe, _ := cmd.StderrPipe()
	err := cmd.Start()
	if err == nil {
		go func() {
			readBuffer := make([]byte, 1500)
			for {
				n, _ := stderrPipe.Read(readBuffer)
				fmt.Fprintf(os.Stderr, "%s", string(readBuffer[0:n]))
			}
		}()
		buffer := make([]byte, 1500)
	} else {
		fmt.Fprintf(os.Stderr, "Failed to spawn process: %v\n", err)
	}
	wg.Done()
}()*/
