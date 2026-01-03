package main

import (
	"bytes"
	"errors"
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

	"golang.org/x/image/draw"
)

const (
	targetWidth  = 320
	targetHeight = 240
	targetAspect = float64(targetWidth) / float64(targetHeight)
)

var (
	listMode          = flag.Bool("list", false, "List available camera devices")
	debugMode         = flag.Bool("debug", false, "Enable debug messages")
	cropWide          = flag.Bool("cropwide", false, "Captures a wide frame and crops it")
	devicePath        = flag.String("device", "", "Camera device ID (empty for default)")
	testMode          = flag.String("test", "", "Test mode: path to input image file")
	listen            = flag.String("listen", ":8080", "HTTP listen address")
	currentFrame      []byte
	currentFrameMutex sync.RWMutex
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
		deviceID := *devicePath
		if deviceID == "" {
			deviceID = DefaultDeviceID
		}
		log.Printf("Starting webcam capture from device: %s", deviceID)
		go captureLoop(deviceID)
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
	fmt.Println("Available camera devices:")
	cameras, err := ListCameras()
	if err != nil {
		if err == ErrNoCameras {
			fmt.Println("  (no cameras found)")
			return
		}
		fmt.Printf("  (error listing cameras: %v)\n", err)
		return
	}
	for _, cam := range cameras {
		fmt.Printf("  %s - %s (%s)\n", cam.DeviceID, cam.Name, cam.Driver)
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

func captureLoop(deviceID string) {
	for {
		if err := captureFromWebcam(deviceID); err != nil {
			log.Printf("Capture error: %v. Retrying in 5 seconds.", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func captureFromWebcam(deviceID string) error {
	cfg := CameraConfig{
		DeviceID:        deviceID,
		PreferredWidth:  targetWidth,
		PreferredHeight: targetHeight,
	}
	if *cropWide {
		// Around 16/9...
		cfg.PreferredWidth = 432
	}
	cam, err := OpenCamera(cfg)
	if err != nil {
		return fmt.Errorf("failed to open camera: %w", err)
	}
	defer cam.Close()

	if err := cam.Start(); err != nil {
		return fmt.Errorf("failed to start camera: %w", err)
	}
	defer cam.Stop()

	log.Printf("Camera started: %s", cam.Name())

	for {
		frame, err := cam.GetFrame()
		if errors.Is(err, ErrNoFrameAvailable) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to get frame: %w", err)
		}
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
