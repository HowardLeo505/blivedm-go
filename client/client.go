package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HowardLeo505/blivedm-go/api"
	"github.com/HowardLeo505/blivedm-go/packet"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

type Client struct {
	conn                *websocket.Conn
	roomID              int
	enterUID            string
	buvid               string
	cookie              string
	userAgent           string
	origin              string
	wscookie            string
	token               string
	host                string
	hostList            []string
	retryCount          int
	eventHandlers       *eventHandlers
	customEventHandlers *customEventHandlers
	cancel              context.CancelFunc
	done                <-chan struct{}
	lock                sync.RWMutex
}

// NewClient 创建一个新的弹幕 client
func NewClient(roomID int, enterUID string, buvid string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		roomID:              roomID,
		enterUID:            enterUID,
		buvid:               buvid,
		retryCount:          0,
		eventHandlers:       &eventHandlers{},
		customEventHandlers: &customEventHandlers{},
		done:                ctx.Done(),
		cancel:              cancel,
		lock:                sync.RWMutex{},
	}
}

// init 初始化 获取真实 RoomID 和 弹幕服务器 host
func (c *Client) init() error {
	roomInfo, err := api.GetRoomInfo(c.roomID)
	// 失败降级
	if err != nil || roomInfo.Code != 0 {
		log.Errorf("room=%d init GetRoomInfo fialed, %s", c.roomID, err)
	}
	c.roomID = roomInfo.Data.RoomId
	if c.host == "" {
		info, err := api.GetDanmuInfo(c.roomID, c.cookie)
		// Workaround for getDanmuInfo API. Error code 352
		if err != nil || info.Code != 0 {
			c.hostList = []string{"broadcastlv.chat.bilibili.com"}
		} else {
			for _, h := range info.Data.HostList {
				c.hostList = append(c.hostList, h.Host)
			}
		}
		c.token = info.Data.Token
	}
	if c.token == "" {
		log.Error("cannot get account token")
		return errors.New("token 获取失败")
	}
	return nil
}

func (c *Client) getWSHeader() http.Header {
	if c.userAgent == "" && c.wscookie == "" && c.origin == "" {
		return nil
	}

	header := http.Header{}

	if c.userAgent != "" {
		header.Set("User-Agent", c.userAgent)
	}
	if c.origin != "" {
		header.Set("Origin", c.origin)
	}
	if c.wscookie != "" {
		header.Set("Cookie", c.wscookie)
	}
	return header
}

func (c *Client) connect() error {
	header := c.getWSHeader()
retry:
	c.host = c.hostList[c.retryCount%len(c.hostList)]
	c.retryCount++
	conn, res, err := websocket.DefaultDialer.Dial(fmt.Sprintf("wss://%s/sub", c.host), header)
	if err != nil {
		log.Errorf("connect dial failed, retry %d times", c.retryCount)
		time.Sleep(2 * time.Second)
		goto retry
	}
	c.conn = conn
	_ = res.Body.Close()
	if err = c.sendEnterPacket(); err != nil {
		log.Errorf("failed to send enter packet, retry %d times", c.retryCount)
		time.Sleep(2 * time.Second)
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
				log.Error("ws message read failed, reconnecting")
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
			c.lock.Lock()
			if err := c.conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
				log.Error(err)
			}
			c.lock.Unlock()
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

// SetHostList
func (c *Client) SetHostList(hostlist []string) {
	c.host = hostlist[0]
	c.hostList = hostlist
}

// SetToken
func (c *Client) SetToken(token string) {
	c.token = token
}

// SetWSCookie
func (c *Client) SetWSCookie(wscookie string) {
	c.wscookie = wscookie
}

func (c *Client) SetOrigin(origin string) {
	c.origin = origin
}

func (c *Client) SetUserAgent(UserAgent string) {
	c.userAgent = UserAgent
}

func (c *Client) SetCookie(cookie string) {
	c.cookie = cookie
}

// UseDefaultHost 使用默认 host broadcastlv.chat.bilibili.com
func (c *Client) UseDefaultHost() {
	c.hostList = []string{"broadcastlv.chat.bilibili.com"}
}

func (c *Client) sendEnterPacket() error {
	//rid, err := strconv.Atoi(c.roomID)
	//if err != nil {
	//	return errors.New("error roomID")
	//}
	uid, err := strconv.Atoi(c.enterUID)
	if err != nil {
		return errors.New("error enterUID")
	}

	pkt := packet.NewEnterPacket(uid, c.buvid, c.roomID, c.token)
	c.lock.Lock()
	defer c.lock.Unlock()
	if err = c.conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
		return err
	}
	log.Debugf("send: EnterPacket")
	return nil
}
