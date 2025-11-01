package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vladimirvivien/go4vl/device"
	"github.com/vladimirvivien/go4vl/v4l2"
	"golang.org/x/image/draw"
)

const (
	targetWidth  = 320
	targetHeight = 240
	targetAspect = float64(targetWidth) / float64(targetHeight)
)

var (
	listMode          = flag.Bool("list", false, "List available v4l2 devices")
	debugMode         = flag.Bool("debug", false, "Enable debug messages")
	cropWide          = flag.Bool("cropwide", false, "Captures a wide frame and crops it")
	devicePath        = flag.String("device", "/dev/video0", "Path to v4l2 device")
	testMode          = flag.String("test", "", "Test mode: path to input image file")
	listen            = flag.String("listen", ":8080", "HTTP listen address")
	currentFrame      []byte
	currentFrameMutex sync.RWMutex
	standardDHT       = []byte{
		0xff, 0xc4, 0x01, 0xa2, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01, 0x01, 0x01,
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02,
		0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x01, 0x00, 0x03,
		0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09,
		0x0a, 0x0b, 0x10, 0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04, 0x03, 0x05,
		0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7d, 0x01, 0x02, 0x03, 0x00, 0x04,
		0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07, 0x22,
		0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08, 0x23, 0x42, 0xb1, 0xc1, 0x15,
		0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36,
		0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a,
		0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66,
		0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a,
		0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95,
		0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8,
		0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2,
		0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5,
		0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7,
		0xe8, 0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9,
		0xfa, 0x11, 0x00, 0x02, 0x01, 0x02, 0x04, 0x04, 0x03, 0x04, 0x07, 0x05,
		0x04, 0x04, 0x00, 0x01, 0x02, 0x77, 0x00, 0x01, 0x02, 0x03, 0x11, 0x04,
		0x05, 0x21, 0x31, 0x06, 0x12, 0x41, 0x51, 0x07, 0x61, 0x71, 0x13, 0x22,
		0x32, 0x81, 0x08, 0x14, 0x42, 0x91, 0xa1, 0xb1, 0xc1, 0x09, 0x23, 0x33,
		0x52, 0xf0, 0x15, 0x62, 0x72, 0xd1, 0x0a, 0x16, 0x24, 0x34, 0xe1, 0x25,
		0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x35, 0x36,
		0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a,
		0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66,
		0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a,
		0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94,
		0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7,
		0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba,
		0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7,
		0xe8, 0xe9, 0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa,
	}
)

func main() {
	flag.Parse()
	if *listMode {
		listDevices()
		return
	}
	if *testMode != "" {
		log.Printf("Running in test mode with image: %s", *testMode)
		testImage, err := loadTestImage(*testMode)
		if err != nil {
			log.Fatalf("Failed to load test image: %v", err)
		}
		currentFrame = processImage(testImage)
		log.Printf("Test image loaded: %dx%d", testImage.Bounds().Dx(), testImage.Bounds().Dy())
	} else {
		log.Printf("Starting webcam capture from %s", *devicePath)
		go captureLoop()
		time.Sleep(500 * time.Millisecond)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/img.jpg", serveImage)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *debugMode {
			log.Printf("%s %s (proto=%q, ua=%q, addr=%q)", r.Method, r.URL, r.Proto, r.UserAgent(), r.RemoteAddr)
		}
		mux.ServeHTTP(w, r)
	})

	if os.Getenv("LISTEN_PID") == strconv.Itoa(os.Getpid()) {
		log.Printf("Starting HTTP server on systemd listener")
		f := os.NewFile(3, "from systemd")
		listener, err := net.FileListener(f)
		if err != nil {
			log.Fatal(err)
		}
		if err := http.Serve(listener, handler); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	} else {
		host, port, err := net.SplitHostPort(*listen)
		if err != nil {
			log.Fatalf("Invalid listen address %q: %v", *listen, err)
		}
		log.Printf("Starting HTTP server on address %s", *listen)
		if host == "" || host == "0.0.0.0" || host == "[::0]" {
			host = "localhost"
		}
		log.Printf("Access at: http://%s:%s/img.jpg", host, port)
		if err := http.ListenAndServe(*listen, handler); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}
}

func listDevices() {
	// TODO: definitely a better way to do this
	fmt.Println("Available v4l2 devices:")
	for i := range 10 {
		devPath := fmt.Sprintf("/dev/video%d", i)
		if _, err := os.Stat(devPath); err != nil {
			continue
		}
		cam, err := device.Open(devPath)
		if err != nil {
			fmt.Printf("  %s - (cannot open: %v)\n", devPath, err)
			continue
		}
		caps := cam.Capability()
		cam.Close()
		fmt.Printf("  %s - %s (%s)\n", devPath, caps.Card, caps.Driver)
	}
}

func loadTestImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func captureLoop() {
	for {
		if err := captureFromWebcam(); err != nil {
			log.Printf("Capture error: %v. Retrying in 5 seconds.", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func captureFromWebcam() error {
	pixFormat := v4l2.PixFormat{
		Width:       320,
		Height:      240,
		PixelFormat: v4l2.PixelFmtMJPEG,
		Field:       v4l2.FieldNone,
	}
	if *cropWide {
		// Around 16/9...
		pixFormat.Width = 432
	}
	cam, err := device.Open(*devicePath, device.WithPixFormat(pixFormat))
	if err != nil {
		return fmt.Errorf("failed to open device: %w", err)
	}
	defer cam.Close()
	if err := cam.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start stream: %w", err)
	}
	defer cam.Stop()
	log.Printf("Camera started: %s", cam.Capability().Card)
	for {
		frame := <-cam.GetOutput()
		frame = mjpegToJFIF(frame)
		if *cropWide {
			img, err := jpeg.Decode(bytes.NewReader(frame))
			if err != nil {
				log.Printf("Invalid frame: %v", err)
				continue
			}
			img = cropAspect(img, targetAspect)
			w := bytes.NewBuffer(frame[:0])
			err = jpeg.Encode(w, img, &jpeg.Options{Quality: 85})
			if err != nil {
				log.Printf("Error encoding cropped frame: %v", err)
				continue
			}
		}
		currentFrameMutex.Lock()
		currentFrame = frame
		currentFrameMutex.Unlock()
	}
}

func mjpegToJFIF(data []byte) []byte {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return data
	}
	// Fix signature for standard JFIF
	if data[6] == 'A' && data[7] == 'V' && data[8] == 'I' && data[9] == '1' {
		data[6], data[7], data[8], data[9] = 'J', 'F', 'I', 'F'
	}
	// Scan for Huffman table, splice standard table if missing.
	i := 2
	for i < len(data)-1 {
		if data[i] != 0xff {
			return data
		}
		marker := data[i+1]
		if marker == 0xc4 {
			return data
		}
		if marker == 0xda {
			result := make([]byte, 0, len(data)+len(standardDHT))
			result = append(result, data[:i]...)
			result = append(result, standardDHT...)
			result = append(result, data[i:]...)
			return result
		}
		if marker == 0x00 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd9) {
			i += 2
			continue
		}
		if i+3 >= len(data) {
			return data
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		i += 2 + segLen
	}
	return data
}

func processImage(img image.Image) []byte {
	return encodeJPEG(resizeImage(cropAspect(img, targetAspect), targetWidth, targetHeight))
}

func cropAspect(img image.Image, targetAspect float64) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	aspect := float64(width) / float64(height)
	cropWidth, cropHeight := width, height
	if aspect > targetAspect {
		cropWidth = int(float64(height) * targetAspect)
	} else {
		cropHeight = int(float64(width) / targetAspect)
	}
	x0 := bounds.Min.X + (width-cropWidth)/2
	y0 := bounds.Min.Y + (height-cropHeight)/2
	cropRect := image.Rect(x0, y0, x0+cropWidth, y0+cropHeight)
	cropped := image.NewRGBA(image.Rect(0, 0, cropWidth, cropHeight))
	draw.Draw(cropped, cropped.Bounds(), img, cropRect.Min, draw.Src)
	return cropped
}

func resizeImage(img image.Image, targetW, targetH int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

func encodeJPEG(img image.Image) []byte {
	var buf strings.Builder
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		log.Printf("Failed to encode JPEG: %v", err)
		return nil
	}
	return []byte(buf.String())
}

func serveImage(w http.ResponseWriter, r *http.Request) {
	var imgData []byte
	currentFrameMutex.RLock()
	imgData = currentFrame
	currentFrameMutex.RUnlock()
	if imgData == nil {
		http.Error(w, "No frame available yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(imgData)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write(imgData)
}
