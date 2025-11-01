package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"

	"github.com/pion/turn/v2"
)

func main() {
	publicIP := flag.String("public-ip", "", "IPv4 Address that TURN can be contacted by.")
	publicIPv6 := flag.String("public-ipv6", "", "IPv6 Address that TURN can be contacted by.")
	port := flag.Int("port", 3478, "Listening port.")
	users := flag.String("users", "", "List of username and password (e.g. \"user=pass,user=pass\")")
	realm := flag.String("realm", "nef.turn", "Realm (defaults to \"pion.ly\")")
	udpPortForm := flag.Int("udp-port-from", 40000, "")
	udpPortTo := flag.Int("udp-port-to", 40199, "")
	flag.Parse()

	if len(*publicIP) == 0 {
		log.Fatalf("'public-ip' is required")
	} else if len(*users) == 0 {
		log.Fatalf("'users' is required")
	}

	// Create a UDP listener to pass into pion/turn
	// pion/turn itself doesn't allocate any UDP sockets, but lets the user pass them in
	// this allows us to add logging, storage or modify inbound/outbound traffic
	udpListener, err := net.ListenPacket("udp4", "0.0.0.0:"+strconv.Itoa(*port))
	if err != nil {
		log.Panicf("Failed to create TURN server IPv4 listener: %s", err)
	}

	var udpListenerIPv6 net.PacketConn
	if len(*publicIPv6) > 0 {
		udpListenerIPv6, err = net.ListenPacket("udp6", "[::]:"+strconv.Itoa(*port))
		if err != nil {
			log.Printf("Failed to create TURN server IPv6 listener: %s", err)
			log.Println("Continuing with IPv4 only")
			udpListenerIPv6 = nil
		}
	}

	// Cache -users flag for easy lookup later
	// If passwords are stored they should be saved to your DB hashed using turn.GenerateAuthKey
	usersMap := map[string][]byte{}
	for _, kv := range regexp.MustCompile(`(\w+)=(\w+)`).FindAllStringSubmatch(*users, -1) {
		usersMap[kv[1]] = turn.GenerateAuthKey(kv[1], *realm, kv[2])
	}

	packetConnConfigs := []turn.PacketConnConfig{
		{
			PacketConn: udpListener,
			RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
				RelayAddress: net.ParseIP(*publicIP), // Claim that we are listening on IP passed by user (This should be your Public IP)
				Address:      "0.0.0.0",              // But actually be listening on every interface
				MinPort:      uint16(*udpPortForm),
				MaxPort:      uint16(*udpPortTo),
			},
		},
	}

	if udpListenerIPv6 != nil {
		packetConnConfigs = append(packetConnConfigs, turn.PacketConnConfig{
			PacketConn: udpListenerIPv6,
			RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
				RelayAddress: net.ParseIP(*publicIPv6),
				Address:      "::",
				MinPort:      uint16(*udpPortForm),
				MaxPort:      uint16(*udpPortTo),
			},
		})
	}

	s, err := turn.NewServer(turn.ServerConfig{
		Realm: *realm,
		// Set AuthHandler callback
		// This is called every time a user tries to authenticate with the TURN server
		// Return the key for that user, or false when no user is found
		AuthHandler: func(username string, realm string, srcAddr net.Addr) ([]byte, bool) {
			if key, ok := usersMap[username]; ok {
				return key, true
			}
			return nil, false
		},
		PacketConnConfigs: packetConnConfigs,
	})
	if err != nil {
		log.Panic(err)
	}

	log.Println("GoTurn is starting")

	// Block until user sends SIGINT or SIGTERM
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	if err = s.Close(); err != nil {
		log.Panic(err)
	}
}
