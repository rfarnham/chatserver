package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	pb "chatserver/proto"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var (
	serverAddr  = flag.String("serverAddr", "localhost:9090", "The server address")
	username    = flag.String("username", "anonymous", "User name")
	noprint     = flag.Bool("noprint", false, "Don't display received messages")
	sleepmillis = flag.Int64("sleepmillis", 0, "Sleep when spamming")
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
		if !*noprint {
			log.Printf("%s) %s: %s", msg.Channel, msg.Sender, msg.Contents)
		}
		if *username == "looper" {
			for {
				log.Printf("infinite looping")
				time.Sleep(1 * time.Second)
			} // infinite loop to stop receiving
		}
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
		msg := pb.ClientMessage{}
		if text == "/quit" {
			log.Printf("Closed stream: %v", stream.CloseSend())
			time.Sleep(1 * time.Second)
			os.Exit(0)
		} else if strings.HasPrefix(text, "/join ") {
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
		} else if strings.HasPrefix(text, "/spam") {
			text = text[len("/spam "):]
			words := strings.Fields(text)
			if len(words) > 0 {
				channel := words[0]
				contents := text[len(channel)+1:]
				log.Printf("spam %s to %s", contents, channel)
				time.Sleep(1 * time.Second)
				for i := 0; ; i++ {
					time.Sleep(time.Duration(*sleepmillis) * time.Millisecond)

					msg.Message = &pb.ClientMessage_BroadcastSend{
						BroadcastSend: &pb.BroadcastSend{
							Channel:  channel,
							Contents: fmt.Sprintf("%d: %s", i, contents),
						},
					}
					err = stream.Send(&msg)
					if err != nil {
						log.Printf("Error sending: %v, backing off", err)
						time.Sleep(3 * time.Second)
					}
				}
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

	stream.Send(&pb.ClientMessage{
		Message: &pb.ClientMessage_Authenticate{
			Authenticate: &pb.Authenticate{
				Sender: *username,
			},
		}})

	log.Printf("Connected to %s", *serverAddr)

	go receiveMessages(stream)
	go sendMessages(stream)

	<-cwait
}
