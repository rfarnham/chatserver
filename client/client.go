package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	pb "chatserver/proto"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var (
	serverAddr = flag.String("serverAddr", "localhost:9090", "The server address")
	username   = flag.String("username", "anonymous", "User name")
)

func receiveMessages(stream pb.ChatServer_ChatClient) {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("%v.receiveMessages(_) = _, %v", stream, err)
		}
		log.Printf("%s: %s", msg.Channel, msg.Contents)
	}
}

func sendMessages(stream pb.ChatServer_ChatClient) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter command: ")
	for {
		text, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal(err)
		}
		text = strings.TrimSpace(text)
		msg := pb.ClientMessage{
			Sender: *username,
		}
		if strings.HasPrefix(text, "/join ") {
			channel := text[len("/join "):]
			msg.Message = &pb.ClientMessage_JoinChannel{
				JoinChannel: &pb.JoinChannel{
					Channel: channel,
				},
			}
		} else if strings.HasPrefix(text, "/leave ") {
			channel := text[len("/leave "):]
			msg.Message = &pb.ClientMessage_LeaveChannel{
				LeaveChannel: &pb.LeaveChannel{
					Channel: channel,
				},
			}
		} else if strings.HasPrefix(text, "/msg ") {
			// TODO: Implement private message
		} else if strings.HasPrefix(text, "/broadcast ") {
			text = text[len("/broadcast "):]
			words := strings.Fields(text)
			if len(words) > 0 {
				channel := words[0]
				contents := text[len(channel)+1:]
				msg.Message = &pb.ClientMessage_BroadcastSend{
					BroadcastSend: &pb.BroadcastSend{
						Channel:  channel,
						Contents: contents,
					},
				}
				log.Printf("broadcast %s to %s", contents, channel)
			}
		} else {
			log.Printf("Unknown command %s", text)
		}
		log.Printf("Sending %v", msg)
		err = stream.Send(&msg)
		if err != nil {
			log.Fatal(err)
		}
	}

}

var cwait = make(chan interface{})

func main() {
	flag.Parse()
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())

	conn, err := grpc.Dial(*serverAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewChatServerClient(conn)
	ctx := context.Background()
	stream, err := client.Chat(ctx)
	if err != nil {
		log.Fatalf("%v.Chat(_) = _, %v", client, err)
	}
	log.Printf("Connected to %s", *serverAddr)

	go receiveMessages(stream)
	go sendMessages(stream)

	<-cwait
}
