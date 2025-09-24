package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HowardLeo505/blivedm-go/api"
	"github.com/HowardLeo505/blivedm-go/packet"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:137.0) Gecko/20100101 Firefox/137.0"

type Client struct {
	conn                *websocket.Conn
	RoomID              int
	EnterUID            string
	Buvid               string
	Cookie              string
	UserAgent           string
	Origin              string
	Referer             string
	WsCookie            string
	Token               string
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
		RoomID:              roomID,
		EnterUID:            enterUID,
		Buvid:               buvid,
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
	if c.Cookie != "" {
		if !strings.Contains(c.Cookie, "bili_jct") || !strings.Contains(c.Cookie, "SESSDATA") {
			log.Errorf("cannot found account token")
			return errors.New("no account token found")
		}
	} else {
		return errors.New("cookie is empty")
	}

	// 短RoomID处理
	if c.RoomID <= 1000 {
		headers := c.getRIDHeader()
		roomInfo, err := api.GetRoomInfo(c.RoomID, &headers)
		// 失败降级
		if err != nil || roomInfo.Code != 0 {
			log.Errorf("room=%d init GetRoomInfo fialed, %s", c.RoomID, err)
		}
		c.RoomID = roomInfo.Data.RoomId
	}

	if c.host == "" {
		headers := c.getDanmuInfoHeader()
		info, err := api.GetDanmuInfo(c.RoomID, &headers)
		// Workaround for getDanmuInfo API. Error code 352
		if err != nil || info.Code != 0 {
			c.hostList = []string{"broadcastlv.chat.bilibili.com"}
		} else {
			for _, h := range info.Data.HostList {
				c.hostList = append(c.hostList, h.Host)
			}
		}
		if c.Token == "" {
			c.Token = info.Data.Token
		}
	}

	if c.Token == "" {
		log.Error("cannot get account Token")
		return errors.New("Token 获取失败")
	}

	return nil
}

func (c *Client) getWSHeader() http.Header {
	if c.WsCookie == "" && c.Origin == "" {
		return nil
	}

	header := http.Header{}

	if c.UserAgent != "" {
		header.Set("User-Agent", c.UserAgent)
	} else {
		header.Set("User-Agent", DefaultUA)
	}

	if c.Origin != "" {
		header.Set("Origin", c.Origin)
	}
	if c.WsCookie != "" {
		header.Set("Cookie", c.WsCookie)
	}
	return header
}

func (c *Client) getRIDHeader() http.Header {
	header := http.Header{}

	if c.UserAgent != "" {
		header.Set("User-Agent", c.UserAgent)
	} else {
		header.Set("User-Agent", DefaultUA)
	}

	if c.Referer != "" {
		header.Set("Referer", c.Referer)
	}

	if c.Origin != "" {
		header.Set("Origin", c.Origin)
	}

	return header
}

func (c *Client) getDanmuInfoHeader() http.Header {
	header := http.Header{}

	if c.UserAgent != "" {
		header.Set("User-Agent", c.UserAgent)
	} else {
		header.Set("User-Agent", DefaultUA)
	}

	header.Set("Cookie", c.Cookie)

	if c.Referer != "" {
		header.Set("Referer", c.Referer)
	}

	if c.Origin != "" {
		header.Set("Origin", c.Origin)
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

// SetHostList 用于从调用方直接注入HostList
func (c *Client) SetHostList(hostlist []string) {
	c.host = hostlist[0]
	c.hostList = hostlist
}

// SetToken 用于从调用方直接注入WS连接Token
func (c *Client) SetToken(token string) {
	c.Token = token
}

// SetWSCookie 用于从调用方直接注入WS连接时的Cookie
func (c *Client) SetWSCookie(wscookie string) {
	c.WsCookie = wscookie
}

func (c *Client) SetOrigin(origin string) {
	c.Origin = origin
}

func (c *Client) SetReferer(referer string) {
	c.Referer = referer
}

// SetUserAgent 用于从调用方直接注入UA，不使用默认的UA
func (c *Client) SetUserAgent(UserAgent string) {
	c.UserAgent = UserAgent
}

func (c *Client) SetCookie(cookie string) {
	c.Cookie = cookie
}

// UseDefaultHost 使用默认 host broadcastlv.chat.bilibili.com
func (c *Client) UseDefaultHost() {
	c.hostList = []string{"broadcastlv.chat.bilibili.com"}
}

func (c *Client) sendEnterPacket() error {
	//rid, err := strconv.Atoi(c.RoomID)
	//if err != nil {
	//	return errors.New("error RoomID")
	//}
	uid, err := strconv.Atoi(c.EnterUID)
	if err != nil {
		return errors.New("error EnterUID")
	}

	pkt := packet.NewEnterPacket(uid, c.Buvid, c.RoomID, c.Token)
	c.lock.Lock()
	defer c.lock.Unlock()
	if err = c.conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
		return err
	}
	log.Debugf("send: EnterPacket")
	return nil
}
