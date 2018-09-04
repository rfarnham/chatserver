package main

import (
	pb "chatserver/proto"
	"flag"
	"fmt"
	"io"
	"log"
	"net"

	"google.golang.org/grpc"
)

var (
	port = flag.Int("port", 9090, "The server port")
)

type event struct {
	conn pb.ChatServer_ChatServer
	msg  *pb.ClientMessage
}

type chatServer struct {
	channels  map[string]map[pb.ChatServer_ChatServer]bool
	eventLoop chan event
}

func newServer() *chatServer {
	s := chatServer{
		channels:  make(map[string]map[pb.ChatServer_ChatServer]bool),
		eventLoop: make(chan event),
	}

	go eventLoopHandler(&s)

	return &s
}

func eventLoopHandler(s *chatServer) {
	for event := range s.eventLoop {
		conn := event.conn
		msg := event.msg
		switch m := msg.Message.(type) {
		case *pb.ClientMessage_JoinChannel:
			s.joinChannel(conn, m.JoinChannel)
		case *pb.ClientMessage_LeaveChannel:
			s.leaveChannel(conn, m.LeaveChannel)
		case *pb.ClientMessage_BroadcastSend:
			s.broadcastSend(msg.Sender, m.BroadcastSend)
		case *pb.ClientMessage_PrivateSend:
			s.privateSend()
		}
	}
}

func (s *chatServer) joinChannel(conn pb.ChatServer_ChatServer, joinChannel *pb.JoinChannel) {
	channel := joinChannel.Channel
	if s.channels[channel] == nil {
		log.Printf("Creating new channel %s", channel)
		s.channels[channel] = make(map[pb.ChatServer_ChatServer]bool)
	}
	log.Printf("%v has joined %s", conn, channel)
	s.channels[channel][conn] = true
}

func (s *chatServer) leaveChannel(conn pb.ChatServer_ChatServer, leaveChannel *pb.LeaveChannel) {
	channel := leaveChannel.Channel
	if s.channels[channel] != nil {
		delete(s.channels[channel], conn)
		log.Printf("%v has left %s", conn, channel)
	}
}

func (s *chatServer) broadcastSend(sender string, broadcastSend *pb.BroadcastSend) {
	channel := broadcastSend.Channel
	contents := broadcastSend.Contents
	serverMsg := pb.ServerMessage{
		Sender:   sender,
		Channel:  channel,
		Contents: contents,
	}
	if s.channels[channel] != nil {
		i := 0
		for conn := range s.channels[channel] {
			err := conn.Send(&serverMsg)
			if err != nil {
				log.Printf("Client %v seems to have dropped out of channel %s", conn, channel)
				delete(s.channels[channel], conn)
			} else {
				i++
			}
		}
		log.Printf("%s sent message '%s' to %d recipients", sender, contents, i)
	}
}

func (s *chatServer) privateSend() {
	// TODO: Implement
	log.Print("Private message not implemented!")
}

func (s *chatServer) Chat(stream pb.ChatServer_ChatServer) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		s.eventLoop <- event{stream, msg}
	}
}

func main() {
	flag.Parse()

	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterChatServerServer(grpcServer, newServer())
	grpcServer.Serve(lis)
}
