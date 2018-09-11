package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"

	pb "chatserver/proto"

	_ "net/http/pprof"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	pprofPort     = flag.Int("pprofPort", 6060, "The pprof port")
	port          = flag.Int("port", 9090, "The server port")
	eventloopSize = flag.Int("eventloop_size", 0, "Maximum number of events in eventloop before blocking")
	outboxSize    = flag.Int("outbox_size", 1000, "Maximum number of outstanding messages to a client. "+
		"If this number is reached, the client is automatically disconnected")
)

type client struct {
	sender  string
	joined  map[string]bool
	errors  chan error
	outbox  chan *pb.ServerMessage
	stream  pb.ChatServer_ChatServer
	ctx     context.Context
	cancel  context.CancelFunc
	id      int32
	dropped bool
}

func (client *client) String() string {
	return fmt.Sprintf("%s (client %d)", client.sender, client.id)
}

type chatServer struct {
	channels     map[string]map[*client]bool
	eventloop    chan event
	nextClientID int32
}

type event struct {
	client *client
	msg    *pb.ClientMessage
}

func (s *chatServer) Chat(stream pb.ChatServer_ChatServer) error {
	sender, err := s.authenticate(stream)
	if err != nil {
		return err
	}

	client := s.newClient(sender, stream)
	log.Printf("%v has connected", client)

	go s.receiveMessages(client)
	go s.sendMessages(client)

	return <-client.errors
}

func (s *chatServer) eventloopHandler() {
	for i := 0; ; i++ {
		if i%10000 == 0 {
			log.Printf("%d messages processed", i)
		}

		event := <-s.eventloop
		client := event.client
		msg := event.msg

		if client.dropped {
			continue
		}

		switch m := msg.Message.(type) {
		case *pb.ClientMessage_JoinChannel:
			s.joinChannel(client, m.JoinChannel)
		case *pb.ClientMessage_LeaveChannel:
			s.leaveChannel(client, m.LeaveChannel)
		case *pb.ClientMessage_BroadcastSend:
			s.broadcastSend(client, m.BroadcastSend)
		default:
			log.Printf("Unsupported message type %T from %v", m, client)
		}
	}
}

func (s *chatServer) authenticate(stream pb.ChatServer_ChatServer) (string, error) {
	msg, err := stream.Recv()
	if err != nil {
		return "", err
	}
	auth := msg.GetAuthenticate()
	if auth == nil {
		return "", status.Error(codes.PermissionDenied, "Client failed to authenticate")
	}

	return auth.Sender, nil
}

func (s *chatServer) joinChannel(c *client, joinChannel *pb.JoinChannel) {
	if c.joined[joinChannel.Channel] {
		log.Printf("%v is already in channel %s", c, joinChannel.Channel)
		return
	}

	channel := s.channels[joinChannel.Channel]
	if channel == nil {
		channel = make(map[*client]bool)
		s.channels[joinChannel.Channel] = channel
		log.Printf("Creating new channel %s", joinChannel.Channel)
	}
	c.joined[joinChannel.Channel] = true
	channel[c] = true

	welcome := pb.BroadcastSend{
		Channel:  joinChannel.Channel,
		Contents: "has joined the channel",
	}
	s.broadcastSend(c, &welcome)
}

func (s *chatServer) leaveChannel(c *client, leaveChannel *pb.LeaveChannel) {
	if !c.joined[leaveChannel.Channel] {
		log.Printf("%v is not in channel %s", c, leaveChannel.Channel)
		return
	}
	delete(c.joined, leaveChannel.Channel)

	channel := s.channels[leaveChannel.Channel]
	delete(channel, c)
	if len(channel) == 0 {
		delete(s.channels, leaveChannel.Channel)
		log.Printf("Deleting empty channel %s", leaveChannel.Channel)
		return
	}

	goodbye := pb.BroadcastSend{
		Channel:  leaveChannel.Channel,
		Contents: "has left the channel",
	}
	s.broadcastSend(c, &goodbye)
}

func (s *chatServer) broadcastSend(c *client, broadcastSend *pb.BroadcastSend) {
	msg := &pb.ServerMessage{
		Sender:   c.sender,
		Channel:  broadcastSend.Channel,
		Contents: broadcastSend.Contents,
	}

	channel := s.channels[broadcastSend.Channel]
	for lis := range channel {
		select {
		case lis.outbox <- msg:
		default:
			log.Printf("Disconnecting unresponsive client %v", lis)
			for ch := range lis.joined {
				delete(s.channels[ch], lis)
			}
			lis.joined = nil
			lis.dropped = true
			lis.cancel()
			lis.errors <- status.Error(codes.ResourceExhausted, "Client is unresponsive")
		}
	}
}

func (s *chatServer) newClient(sender string, stream pb.ChatServer_ChatServer) *client {
	ctx, cancel := context.WithCancel(stream.Context())
	return &client{
		sender: sender,
		joined: make(map[string]bool),
		errors: make(chan error, 3), // buffered for sender, receiver, and eventloop errors
		outbox: make(chan *pb.ServerMessage, *outboxSize),
		stream: stream,
		ctx:    ctx,
		cancel: cancel,
		id:     atomic.AddInt32(&s.nextClientID, 1),
	}
}

func (s *chatServer) receiveMessages(client *client) {
	for {
		msg, err := client.stream.Recv()
		if err == io.EOF {
			log.Printf("%v closing connection", client)
			client.errors <- err
			return
		}
		if err != nil {
			log.Printf("%v receive error: %v", client, err)
			client.errors <- err
			return
		}

		s.eventloop <- event{client, msg}
	}
}

func (s *chatServer) sendMessages(client *client) {
	for {
		select {
		case msg := <-client.outbox:
			err := client.stream.Send(msg)
			if err != nil {
				log.Printf("%v send error: %v", client, err)
				client.errors <- err
				return
			}
		case <-client.ctx.Done():
			log.Printf("%v sender exiting: cancelled context", client)
			return
		}
	}
}

func newChatServer() *chatServer {
	return &chatServer{
		channels:  make(map[string]map[*client]bool),
		eventloop: make(chan event, *eventloopSize),
	}
}

func main() {
	flag.Parse()

	go func() {
		err := http.ListenAndServe(fmt.Sprintf("localhost:%d", *pprofPort), nil)
		if err != nil {
			log.Fatalf("Failed to bind pprof: %v", err)
		}
	}()

	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", *port))
	if err != nil {
		log.Fatalf("Failed to bind tcp: %v", err)
	}

	grpcServer := grpc.NewServer()
	chatServer := newChatServer()
	pb.RegisterChatServerServer(grpcServer, chatServer)

	go chatServer.eventloopHandler()
	grpcServer.Serve(lis)
}
