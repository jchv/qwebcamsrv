//go:build linux

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vladimirvivien/go4vl/device"
	"github.com/vladimirvivien/go4vl/v4l2"
)

const DefaultDeviceID = "/dev/video0"

func ListCameras() ([]CameraInfo, error) {
	var cameras []CameraInfo
	for i := range 10 {
		devPath := fmt.Sprintf("/dev/video%d", i)
		if _, err := os.Stat(devPath); err != nil {
			continue
		}
		cam, err := device.Open(devPath)
		if err != nil {
			cameras = append(cameras, CameraInfo{
				DeviceID: devPath,
				Name:     fmt.Sprintf("Video Device %d (unavailable)", i),
				Driver:   "unknown",
			})
			continue
		}
		caps := cam.Capability()
		cam.Close()
		cameras = append(cameras, CameraInfo{
			DeviceID: devPath,
			Name:     caps.Card,
			Driver:   caps.Driver,
		})
	}
	if len(cameras) == 0 {
		return nil, ErrNoCameras
	}
	return cameras, nil
}

func OpenCamera(config CameraConfig) (Camera, error) {
	width := config.PreferredWidth
	height := config.PreferredHeight
	if width <= 0 {
		width = 320
	}
	if height <= 0 {
		height = 240
	}

	cam, err := device.Open(config.DeviceID, device.WithPixFormat(v4l2.PixFormat{
		Width:       uint32(width),
		Height:      uint32(height),
		PixelFormat: v4l2.PixelFmtMJPEG,
		Field:       v4l2.FieldNone,
	}))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCameraNotFound
		}
		return nil, fmt.Errorf("failed to open camera: %w", err)
	}

	return &linuxCamera{
		device:   cam,
		deviceID: config.DeviceID,
		ctx:      context.Background(),
	}, nil
}

type linuxCamera struct {
	device   *device.Device
	deviceID string
	ctx      context.Context
	cancel   context.CancelFunc
	output   <-chan []byte
}

func (c *linuxCamera) Start() error {
	c.ctx, c.cancel = context.WithCancel(context.Background())
	if err := c.device.Start(c.ctx); err != nil {
		return fmt.Errorf("failed to start camera: %w", err)
	}
	c.output = c.device.GetOutput()
	return nil
}

func (c *linuxCamera) Stop() error {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	return c.device.Stop()
}

func (c *linuxCamera) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	return c.device.Close()
}

func (c *linuxCamera) GetFrame() ([]byte, error) {
	select {
	case frame := <-c.output:
		return mjpegToJFIF(frame), nil
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}

func (c *linuxCamera) Name() string {
	return c.device.Capability().Card
}
