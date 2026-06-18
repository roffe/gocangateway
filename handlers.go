package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/roffe/gocan"
	"github.com/roffe/gocan/proto"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func adapterConfigFromContext(ctx context.Context) (string, *gocan.AdapterConfig, error) {
	md, exists := metadata.FromIncomingContext(ctx)
	if !exists {
		return "", nil, errors.New("connect metadata not found")
	}

	// require returns the first value for key or an error, guarding against a
	// version-skewed client that omits a field (md[key][0] would otherwise panic).
	require := func(key string) (string, error) {
		if vals := md.Get(key); len(vals) > 0 {
			return vals[0], nil
		}
		return "", fmt.Errorf("missing required metadata %q", key)
	}

	adapter, err := require("adapter")
	if err != nil {
		return "", nil, err
	}
	port, err := require("port")
	if err != nil {
		return "", nil, err
	}
	debugStr, err := require("debug")
	if err != nil {
		return "", nil, err
	}
	dbg, err := strconv.ParseBool(debugStr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid debug: %w", err)
	}
	baudrateStr, err := require("port_baudrate")
	if err != nil {
		return "", nil, err
	}
	portBaudrate, err := strconv.Atoi(baudrateStr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid port_baudrate: %w", err)
	}
	canrateStr, err := require("canrate")
	if err != nil {
		return "", nil, err
	}
	canrate, err := strconv.ParseFloat(canrateStr, 64)
	if err != nil {
		return "", nil, fmt.Errorf("invalid canrate: %w", err)
	}
	useExtendedIDStr, err := require("useextendedid")
	if err != nil {
		return "", nil, err
	}
	useExtendedID, err := strconv.ParseBool(useExtendedIDStr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid useextendedid: %w", err)
	}

	additionalConfig := make(map[string]string)
	if v := md.Get("minversion"); len(v) > 0 {
		additionalConfig["minversion"] = v[0]
	}

	var canFilter []uint32
	if v := md.Get("canfilter"); len(v) > 0 {
		canFilter = parseFilters(strings.Split(v[0], ","))
	}

	return adapter, &gocan.AdapterConfig{
		Debug:            dbg,
		Port:             port,
		PortBaudrate:     portBaudrate,
		CANRate:          canrate,
		CANFilter:        canFilter,
		UseExtendedID:    useExtendedID,
		AdditionalConfig: additionalConfig,
		PrintVersion:     true,
	}, nil
}

func parseFilters(filters []string) []uint32 {
	var canfilters []uint32
	for _, id := range filters {
		i, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			log.Printf("invalid canfilter: %v", err)
			continue
		}
		canfilters = append(canfilters, uint32(i))
	}
	return canfilters
}

type gocanStream = grpc.BidiStreamingServer[proto.CANFrame, proto.StreamMessage]

// sendEvent forwards a typed event/error to the client over the stream.
func sendEvent(srv gocanStream, level proto.EventLevel, msg string) error {
	return srv.Send(&proto.StreamMessage{
		Payload: &proto.StreamMessage_Event{
			Event: &proto.Event{Level: level, Message: msg},
		},
	})
}

// eventLevel maps a gocan adapter event type to the wire event level.
func eventLevel(t gocan.EventType) proto.EventLevel {
	switch t {
	case gocan.EventTypeFatal:
		return proto.EventLevel_EVENT_FATAL
	case gocan.EventTypeError:
		return proto.EventLevel_EVENT_ERROR
	case gocan.EventTypeWarning:
		return proto.EventLevel_EVENT_WARN
	case gocan.EventTypeDebug:
		return proto.EventLevel_EVENT_DEBUG
	default:
		return proto.EventLevel_EVENT_INFO
	}
}

func (s *Server) SendCommand(ctx context.Context, in *proto.Command) (*proto.CommandResponse, error) {
	switch {
	case bytes.Equal(in.GetData(), []byte("ping")):
		return &proto.CommandResponse{Data: []byte("pong")}, nil
	case bytes.Equal(in.GetData(), []byte("quit")):
		if !ignoreQuit {
			go func() {
				log.Println("stopping server")
				time.Sleep(10 * time.Millisecond)
				if err := s.Close(); err != nil {
					log.Fatalf("failed to close server: %v", err)
				}

			}()
		}
		return &proto.CommandResponse{Data: []byte("OK")}, nil
	default:
		return nil, fmt.Errorf("unknown command: %s", in.GetData())
	}
}

func (s *Server) Stream(srv gocanStream) error {
	// gctx, cancel := context.WithCancel(srv.Context())
	gctx := srv.Context()

	adaptername, adapterConfig, err := adapterConfigFromContext(gctx)
	if err != nil {
		return fmt.Errorf("failed to create adapter config: %w", err)
	}

	dev, err := gocan.NewAdapter(adaptername, adapterConfig)
	if err != nil {
		return fmt.Errorf("failed to create adapter: %w", err)
	}

	errg, ctx := errgroup.WithContext(gctx)

	log.Printf("connecting to %s", adaptername)
	if err := dev.Open(ctx); err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer dev.Close()
	log.Printf("%s connected @ %g kbp/s", adaptername, adapterConfig.CANRate)
	defer log.Printf("%s disconnected", adaptername)
	if err := sendEvent(srv, proto.EventLevel_EVENT_INFO, "OK"); err != nil {
		return fmt.Errorf("failed to send init response: %w", err)
	}

	// send mesage from canbus adapter to IPC
	errg.Go(s.recvManager(ctx, srv, dev))
	// send message from IPC to canbus adapter
	go s.sendManager(srv, dev)()

	if err := errg.Wait(); err != nil {
		if err == context.Canceled {
			return nil
		}
		_ = sendEvent(srv, proto.EventLevel_EVENT_FATAL, err.Error())
		log.Println("stream error:", err)
		return err
	}
	return nil
}

func (s *Server) recvManager(ctx context.Context, srv gocanStream, dev gocan.Adapter) func() error {
	return func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case e := <-dev.Event():
				if err := sendEvent(srv, eventLevel(e.Type), e.Raw()); err != nil {
					return err
				}
			case err := <-dev.Err():
				log.Println("adapter error:", err)
				return fmt.Errorf("adapter error: %w", err)
			case msg, ok := <-dev.Recv():
				if !ok {
					return errors.New("adapter recv channel closed")
				}
				if msg == nil {
					log.Println("adapter nil message")
					continue
				}
				if err := s.recvMessage(srv, msg); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Server) recvMessage(srv gocanStream, msg *gocan.CANFrame) error {
	mmsg := &proto.StreamMessage{
		Payload: &proto.StreamMessage_Frame{
			Frame: &proto.CANFrame{
				Id:        msg.Identifier,
				Data:      msg.Data,
				FrameType: proto.CANFrameTypeEnum(msg.FrameType.Type),
				Responses: uint32(msg.FrameType.Responses),
			},
		},
	}
	if err := srv.Send(mmsg); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}

func (s *Server) sendManager(srv gocanStream, dev gocan.Adapter) func() error {
	return func() error {
		for {
			msg, err := srv.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil // Client closed connection
				}
				if e, ok := status.FromError(err); ok {
					switch e.Code() {
					case codes.Canceled:
						return nil
					//case codes.PermissionDenied:
					//	fmt.Println(e.Message()) // this will print PERMISSION_DENIED_TEST
					//case codes.Internal:
					//	fmt.Println("Has Internal Error")
					case codes.Aborted:
						log.Println("gRPC Aborted the call")
						return nil
						//default:
						//	log.Println(e.Code(), e.Message())
					}
				}
				return fmt.Errorf("sendManager recv error: %w", err) // Something unexpected happened
			}
			s.sendMessage(srv, dev, msg)
		}
	}
}

func (s *Server) sendMessage(srv gocanStream, dev gocan.Adapter, msg *proto.CANFrame) {
	frame := gocan.NewFrame(msg.GetId(), msg.GetData(), gocan.CANFrameType{
		Type:      gocan.ResponseType(msg.GetFrameType()),
		Responses: int(msg.GetResponses()),
	})
	select {
	case dev.Send() <- frame:
	default:
		_ = sendEvent(srv, proto.EventLevel_EVENT_ERROR, "adapter send buffer full")
	}
}

func (s *Server) GetAdapters(ctx context.Context, _ *emptypb.Empty) (*proto.Adapters, error) {
	//md, _ := metadata.FromIncomingContext(ctx)
	//for k, v := range md {
	//	log.Printf("metadata: %s: %v", k, v)
	//}
	var adapters []*proto.AdapterInfo
	for _, a := range gocan.ListAdapters() {
		adapter := &proto.AdapterInfo{
			Name:        a.Name,
			Description: a.Description,
			Capabilities: &proto.AdapterCapabilities{
				HSCAN: a.Capabilities.HSCAN,
				KLine: a.Capabilities.KLine,
				SWCAN: a.Capabilities.SWCAN,
			},
			RequireSerialPort: a.RequiresSerialPort,
		}
		adapters = append(adapters, adapter)
	}
	return &proto.Adapters{
		Adapters: adapters,
	}, nil
}

func (s *Server) GetSerialPorts(ctx context.Context, _ *emptypb.Empty) (*proto.SerialPorts, error) {
	// Serial ports are enumerated client-side; return an empty (non-nil) list so
	// callers never receive a nil message alongside a nil error.
	return &proto.SerialPorts{}, nil
}
