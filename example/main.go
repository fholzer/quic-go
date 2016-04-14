package main

import (
	"bytes"
	"fmt"
	"net"
	"os"

	"github.com/lucas-clemente/quic-go"
	"github.com/lucas-clemente/quic-go/crypto"
	"github.com/lucas-clemente/quic-go/utils"
)

const (
	// QuicVersion32Bytes is the QUIC protocol version
	QuicVersionNumber32 = 32
)

func main() {
	QuicVersion32, _ := utils.ReadUint32BigEndian(bytes.NewReader([]byte{'Q', '0', 48 + (QuicVersionNumber32/10)%10, 48 + QuicVersionNumber32%10}))

	path := os.Getenv("GOPATH") + "/src/github.com/lucas-clemente/quic-go/example/"
	keyData, err := crypto.LoadKeyData(path+"cert.der", path+"key.der")
	if err != nil {
		panic(err)
	}

	addr, err := net.ResolveUDPAddr("udp", "localhost:6121")
	if err != nil {
		panic(err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		panic(err)
	}

	data := make([]byte, 0x10000)
	n, remoteAddr, err := conn.ReadFromUDP(data)
	if err != nil {
		panic(err)
	}
	data = data[:n]
	r := bytes.NewReader(data)

	fmt.Printf("Remote addr: %v\n", remoteAddr)

	publicHeader, err := quic.ParsePublicHeader(r)
	if err != nil {
		panic(err)
	}

	// send Version Negotiation Packet if the client is speaking a different protocol version
	if publicHeader.VersionFlag && publicHeader.QuicVersion != QuicVersion32 {
		fmt.Println("Sending VersionNegotiationPacket")
		fullReply := &bytes.Buffer{}
		responsePublicHeader := quic.PublicHeader{ConnectionID: publicHeader.ConnectionID, PacketNumber: 1, VersionFlag: true}
		err = responsePublicHeader.WritePublicHeader(fullReply)
		if err != nil {
			panic(err)
		}
		utils.WriteUint32BigEndian(fullReply, QuicVersion32)
		conn.WriteToUDP(fullReply.Bytes(), remoteAddr)
	}

	nullAEAD := &crypto.NullAEAD{}
	r, err = nullAEAD.Open(0, data[0:int(r.Size())-r.Len()], r)
	if err != nil {
		panic(err)
	}

	privateFlag, err := r.ReadByte()
	if err != nil {
		panic(err)
	}

	var entropyAcc quic.EntropyAccumulator
	entropyAcc.Add(publicHeader.PacketNumber, privateFlag&0x01 > 0)

	frame, err := quic.ParseStreamFrame(r)
	if err != nil {
		panic(err)
	}

	messageTag, cryptoData, err := quic.ParseCryptoMessage(frame.Data)
	if err != nil {
		panic(err)
	}

	if messageTag != quic.TagCHLO {
		panic("expected CHLO")
	}

	fmt.Printf("Talking to: %q\n", cryptoData[quic.TagUAID])

	kex := crypto.NewCurve25519KEX()

	serverConfig := &bytes.Buffer{}
	quic.WriteCryptoMessage(serverConfig, quic.TagSCFG, map[quic.Tag][]byte{
		quic.TagSCID: []byte{0xC5, 0x1C, 0x73, 0x6B, 0x8F, 0x48, 0x49, 0xAE, 0xB3, 0x00, 0xA2, 0xD4, 0x4B, 0xA0, 0xCF, 0xDF},
		quic.TagKEXS: []byte("C255"),
		quic.TagAEAD: []byte("AESG"),
		quic.TagPUBS: append([]byte{0x20, 0x00, 0x00}, kex.PublicKey()...),
		quic.TagOBIT: []byte{0x0, 0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7},
		quic.TagEXPY: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		quic.TagVER:  []byte("Q032"),
	})

	proof, err := keyData.SignServerProof(frame.Data, serverConfig.Bytes())
	if err != nil {
		panic(err)
	}
	serverReply := &bytes.Buffer{}
	quic.WriteCryptoMessage(serverReply, quic.TagREJ, map[quic.Tag][]byte{
		quic.TagSCFG: serverConfig.Bytes(),
		quic.TagCERT: keyData.GetCERTdata(),
		quic.TagPROF: proof,
	})

	replyFrame := &bytes.Buffer{}
	replyFrame.WriteByte(0) // Private header
	quic.WriteAckFrame(replyFrame, &quic.AckFrame{
		Entropy:         entropyAcc.Get(),
		LargestObserved: 1,
	})
	quic.WriteStreamFrame(replyFrame, &quic.StreamFrame{
		StreamID: 1,
		Data:     serverReply.Bytes(),
	})

	fullReply := &bytes.Buffer{}
	responsePublicHeader := quic.PublicHeader{ConnectionID: publicHeader.ConnectionID, PacketNumber: 1}
	fmt.Println(responsePublicHeader)
	err = responsePublicHeader.WritePublicHeader(fullReply)
	if err != nil {
		panic(err)
	}

	nullAEAD.Seal(0, fullReply, fullReply.Bytes(), replyFrame.Bytes())

	conn.WriteToUDP(fullReply.Bytes(), remoteAddr)

	n, _, err = conn.ReadFromUDP(data)
	if err != nil {
		panic(err)
	}
	data = data[:n]
	r = bytes.NewReader(data)

	publicHeader, err = quic.ParsePublicHeader(r)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%#v\n", publicHeader)
}