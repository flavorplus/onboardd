//go:build linux

package recovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	gpioV2GetLineIOCTL       = 0xc250b407
	gpioV2LineGetValuesIOCTL = 0xc010b40e

	gpioV2LineFlagInput       = 1 << 2
	gpioV2LineFlagEdgeRising  = 1 << 4
	gpioV2LineFlagEdgeFalling = 1 << 5
	gpioV2LineFlagBiasPullUp  = 1 << 8

	gpioV2LineAttrDebounce = 3
	gpioV2EventRising      = 1
	gpioV2EventFalling     = 2
	gpioPollMilliseconds   = 250
)

type gpioV2LineAttribute struct {
	id      uint32
	padding uint32
	value   uint64
}

type gpioV2LineConfigAttribute struct {
	attribute gpioV2LineAttribute
	mask      uint64
}

type gpioV2LineConfig struct {
	flags      uint64
	numAttrs   uint32
	padding    [5]uint32
	attributes [10]gpioV2LineConfigAttribute
}

type gpioV2LineRequest struct {
	offsets         [64]uint32
	consumer        [32]byte
	config          gpioV2LineConfig
	numLines        uint32
	eventBufferSize uint32
	padding         [5]uint32
	fd              int32
}

type gpioV2LineValues struct {
	bits uint64
	mask uint64
}

type gpioV2LineEvent struct {
	timestampNS  uint64
	id           uint32
	offset       uint32
	sequence     uint32
	lineSequence uint32
	padding      [6]uint32
}

var (
	_ [592 - int(unsafe.Sizeof(gpioV2LineRequest{}))]byte
	_ [int(unsafe.Sizeof(gpioV2LineRequest{})) - 592]byte
	_ [16 - int(unsafe.Sizeof(gpioV2LineValues{}))]byte
	_ [int(unsafe.Sizeof(gpioV2LineValues{})) - 16]byte
	_ [48 - int(unsafe.Sizeof(gpioV2LineEvent{}))]byte
	_ [int(unsafe.Sizeof(gpioV2LineEvent{})) - 48]byte
)

var errGPIOClosed = errors.New("gpio line is closed")

type gpioButtonSource struct {
	fd             int
	initialPressed bool
	close          sync.Once
	closeErr       error
}

func openButtonSource(options ButtonOptions) (source buttonSource, resultErr error) {
	chipFD, err := unix.Open(options.ChipPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open gpio chip %q: %w", options.ChipPath, err)
	}
	defer func() {
		if closeErr := unix.Close(chipFD); closeErr != nil {
			if source != nil {
				closeErr = errors.Join(closeErr, source.Close())
				source = nil
			}
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close gpio chip %q: %w", options.ChipPath, closeErr),
			)
		}
	}()

	debounceMicroseconds := options.Debounce.Microseconds()
	if debounceMicroseconds <= 0 || debounceMicroseconds > int64(^uint32(0)) {
		return nil, errors.New("gpio recovery debounce duration is outside kernel range")
	}
	request := gpioV2LineRequest{
		config: gpioV2LineConfig{
			flags: gpioV2LineFlagInput |
				gpioV2LineFlagEdgeRising |
				gpioV2LineFlagEdgeFalling |
				gpioV2LineFlagBiasPullUp,
			numAttrs: 1,
		},
		numLines:        1,
		eventBufferSize: 16,
		fd:              -1,
	}
	request.offsets[0] = options.Line
	copy(request.consumer[:], "onboardd-recovery")
	request.config.attributes[0] = gpioV2LineConfigAttribute{
		attribute: gpioV2LineAttribute{
			id:    gpioV2LineAttrDebounce,
			value: uint64(debounceMicroseconds),
		},
		mask: 1,
	}
	if err := gpioIOCTL(chipFD, gpioV2GetLineIOCTL, unsafe.Pointer(&request)); err != nil {
		return nil, fmt.Errorf("request gpio line %d from %q: %w", options.Line, options.ChipPath, err)
	}
	if request.fd < 0 {
		return nil, errors.New("kernel returned an invalid gpio line descriptor")
	}

	values := gpioV2LineValues{mask: 1}
	if err := gpioIOCTL(int(request.fd), gpioV2LineGetValuesIOCTL, unsafe.Pointer(&values)); err != nil {
		closeErr := unix.Close(int(request.fd))
		if closeErr != nil {
			closeErr = fmt.Errorf("close gpio line after initial read failure: %w", closeErr)
		}
		return nil, errors.Join(
			fmt.Errorf("read initial gpio line %d value: %w", options.Line, err),
			closeErr,
		)
	}
	source = &gpioButtonSource{
		fd:             int(request.fd),
		initialPressed: values.bits&1 == 0,
	}
	return source, nil
}

func (s *gpioButtonSource) InitialPressed() bool {
	return s.initialPressed
}

func (s *gpioButtonSource) Run(ctx context.Context) (buttonStream, error) {
	events := make(chan buttonEvent, 1)
	errorsOut := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		defer close(errorsOut)
		defer s.Close()
		for {
			event, err := s.next(ctx)
			if err != nil {
				if ctx.Err() == nil && !errors.Is(err, errGPIOClosed) {
					select {
					case errorsOut <- err:
					case <-ctx.Done():
					}
				}
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return buttonStream{events: events, errors: errorsOut, done: done}, nil
}

func (s *gpioButtonSource) next(ctx context.Context) (buttonEvent, error) {
	for {
		if ctx.Err() != nil {
			return buttonEventUnknown, ctx.Err()
		}
		poll := []unix.PollFd{{Fd: int32(s.fd), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, gpioPollMilliseconds)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			if errors.Is(err, unix.EBADF) {
				return buttonEventUnknown, errGPIOClosed
			}
			return buttonEventUnknown, fmt.Errorf("poll gpio line: %w", err)
		}
		if ready == 0 {
			continue
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return buttonEventUnknown, errors.New("gpio line poll reported a terminal condition")
		}
		var event gpioV2LineEvent
		buffer := unsafe.Slice((*byte)(unsafe.Pointer(&event)), int(unsafe.Sizeof(event)))
		read, err := unix.Read(s.fd, buffer)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			if errors.Is(err, unix.EBADF) {
				return buttonEventUnknown, errGPIOClosed
			}
			return buttonEventUnknown, fmt.Errorf("read gpio line event: %w", err)
		}
		if read != len(buffer) {
			return buttonEventUnknown, io.ErrUnexpectedEOF
		}
		switch event.id {
		case gpioV2EventFalling:
			return buttonEventPressed, nil
		case gpioV2EventRising:
			return buttonEventReleased, nil
		default:
			return buttonEventUnknown, fmt.Errorf("unsupported gpio edge event %d", event.id)
		}
	}
}

func (s *gpioButtonSource) Close() error {
	s.close.Do(func() {
		s.closeErr = unix.Close(s.fd)
		if errors.Is(s.closeErr, unix.EBADF) {
			s.closeErr = nil
		}
	})
	return s.closeErr
}

func gpioIOCTL(fd int, request uintptr, argument unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(argument))
	if errno != 0 {
		return errno
	}
	return nil
}
