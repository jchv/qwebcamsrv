package main

import (
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
	listMode        = flag.Bool("list", false, "List available v4l2 devices")
	devicePath      = flag.String("device", "/dev/video0", "Path to v4l2 device")
	testMode        = flag.String("test", "", "Test mode: path to input image file")
	listen          = flag.String("listen", ":8080", "HTTP listen address")
	currentFrame    []byte
	currentFrameMux sync.RWMutex
	testImage       image.Image
)

func main() {
	flag.Parse()
	if *listMode {
		listDevices()
		return
	}
	if *testMode != "" {
		log.Printf("Running in test mode with image: %s", *testMode)
		var err error
		testImage, err = loadTestImage(*testMode)
		if err != nil {
			log.Fatalf("Failed to load test image: %v", err)
		}
		log.Printf("Test image loaded: %dx%d", testImage.Bounds().Dx(), testImage.Bounds().Dy())
	} else {
		log.Printf("Starting webcam capture from %s", *devicePath)
		go captureLoop()
		time.Sleep(500 * time.Millisecond)
	}
	http.HandleFunc("/img.jpg", serveImage)
	host, port, err := net.SplitHostPort(*listen)
	if err != nil {
		log.Fatalf("Invalid listen address %q: %v", *listen, err)
	}
	log.Printf("Starting HTTP server on address %s", *listen)
	if host == "" || host == "0.0.0.0" || host == "[::0]" {
		host = "localhost"
	}
	log.Printf("Access at: http://%s:%s/img.jpg", host, port)

	if err := http.ListenAndServe(*listen, nil); err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
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
	cam, err := device.Open(*devicePath, device.WithPixFormat(v4l2.PixFormat{
		Width:       640,
		Height:      480,
		PixelFormat: v4l2.PixelFmtMJPEG,
		Field:       v4l2.FieldNone,
	}))
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
		img, err := jpeg.Decode(strings.NewReader(string(frame)))
		if err != nil {
			log.Printf("Failed to decode frame: %v", err)
			continue
		}
		processed := processImage(img)
		currentFrameMux.Lock()
		currentFrame = processed
		currentFrameMux.Unlock()
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
	if testImage != nil {
		imgData = processImage(testImage)
	} else {
		currentFrameMux.RLock()
		imgData = currentFrame
		currentFrameMux.RUnlock()
		if imgData == nil {
			http.Error(w, "No frame available yet", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write(imgData)
}
