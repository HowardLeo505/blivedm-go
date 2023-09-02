package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/HowardLeo505/blivedm-go/api"
	"github.com/HowardLeo505/blivedm-go/packet"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

type Client struct {
	conn                *websocket.Conn
	roomID              string
	tempID              string
	enterUID            string
	buvid               string
	userAgent           string
	origin              string
	cookie              string
	wscookie            string
	token               string
	host                string
	hostList            []string
	eventHandlers       *eventHandlers
	customEventHandlers *customEventHandlers
	cancel              context.CancelFunc
	done                <-chan struct{}
}

// NewClient 创建一个新的弹幕 client
func NewClient(roomID string, enterUID string, buvid string, userAgent string, origin string, cookie string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		tempID:              roomID,
		enterUID:            enterUID,
		buvid:               buvid,
		userAgent:           userAgent,
		origin:              origin,
		cookie:              cookie,
		eventHandlers:       &eventHandlers{},
		customEventHandlers: &customEventHandlers{},
		done:                ctx.Done(),
		cancel:              cancel,
	}
}

// init 初始化 获取真实 roomID 和 弹幕服务器 host
func (c *Client) init() error {
	rid, _ := strconv.Atoi(c.tempID)
	// 处理 shortID
	if rid <= 1000 && c.roomID == "" {
		realID, err := api.GetRoomRealID(c.tempID)
		if err != nil {
			return err
		}
		c.roomID = realID
	} else {
		c.roomID = c.tempID
	}
	if c.host == "" {
		info, err := api.GetDanmuInfo(c.roomID, c.cookie)
		if err != nil {
			c.hostList = []string{"broadcastlv.chat.bilibili.com"}
		} else {
			for _, h := range info.Data.HostList {
				c.hostList = append(c.hostList, h.Host)
			}
		}
		c.token = info.Data.Token
	}
	return nil
}

func (c *Client) getWSHeader() http.Header {
	if c.userAgent == "" && c.wscookie == "" {
		return nil
	}

	header := http.Header{}

	if c.userAgent != "" {
		header.Set("User-Agent", c.userAgent)
	}
	if c.wscookie != "" {
		header.Set("Cookie", c.wscookie)
	}
	return header
}

func (c *Client) connect() error {
	retryCount := 0
retry:
	// 随着重连会自动切换弹幕服务器
	c.host = c.hostList[retryCount%len(c.hostList)]
	retryCount++
	header := c.getWSHeader()
	conn, res, err := websocket.DefaultDialer.Dial(fmt.Sprintf("wss://%s/sub", c.host), header)
	if err != nil {
		log.Errorf("connect dial failed, retry %d times", retryCount)
		time.Sleep(2 * time.Second)
		goto retry
	}
	c.conn = conn
	res.Body.Close()
	if err = c.sendEnterPacket(); err != nil {
		log.Errorf("failed to send enter packet, retry %d times", retryCount)
		goto retry
	}
	if _, _, err = c.conn.ReadMessage(); fmt.Sprintf("%+v", err) == "websocket: close 1006 (abnormal closure): unexpected EOF" {
		log.Info("request server busy, retrying other server")
		goto retry
	}
	return nil
}

func (c *Client) wsLoop() {
	for {
		select {
		case <-c.done:
			log.Debug("current client closed")
			return
		default:
			msgType, data, err := c.conn.ReadMessage()
			if err != nil {
				log.Info("reconnect")
				time.Sleep(time.Duration(3) * time.Millisecond)
				_ = c.connect()
				continue
			}
			if msgType != websocket.BinaryMessage {
				log.Error("packet not binary")
				continue
			}
			for _, pkt := range packet.DecodePacket(data).Parse() {
				go c.Handle(pkt)
			}
		}
	}
}

func (c *Client) heartBeatLoop() {
	pkt := packet.NewHeartBeatPacket()
	for {
		select {
		case <-c.done:
			return
		case <-time.After(30 * time.Second):
			if err := c.conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
				log.Error(err)
			}
			log.Debug("send: HeartBeat")
		}
	}
}

// Start 启动弹幕 Client 初始化并连接 ws、发送心跳包
func (c *Client) Start() error {
	if err := c.init(); err != nil {
		return err
	}
	if err := c.connect(); err != nil {
		return err
	}
	go c.wsLoop()
	go c.heartBeatLoop()
	return nil
}

// Stop 停止弹幕 Client
func (c *Client) Stop() {
	c.cancel()
}

func (c *Client) SetHostList(hostlist []string) {
	c.host = hostlist[0]
	c.hostList = hostlist
}

func (c *Client) SetToken(token string) {
	c.token = token
}

func (c *Client) SetWSCookie(wscookie string) {
	c.wscookie = wscookie
}

// UseDefaultHost 使用默认 host broadcastlv.chat.bilibili.com
func (c *Client) UseDefaultHost() {
	c.hostList = []string{"broadcastlv.chat.bilibili.com"}
}

func (c *Client) sendEnterPacket() error {
	rid, err := strconv.Atoi(c.roomID)
	if err != nil {
		return errors.New("error roomID")
	}
	uid, err := strconv.Atoi(c.enterUID)
	if err != nil {
		return errors.New("error enterUID")
	}
	pkt := packet.NewEnterPacket(uid, c.buvid, rid, c.token)
	if err = c.conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
		return err
	}
	log.Debugf("send: EnterPacket")
	return nil
}
