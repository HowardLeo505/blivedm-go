package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/tidwall/gjson"
)

// RoomInfo
// api https://api.live.bilibili.com/room/v1/Room/room_init?id={} response
type RoomInfo struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Message string `json:"message"`
	Data    struct {
		RoomId      int   `json:"room_id"`
		ShortId     int   `json:"short_id"`
		Uid         int   `json:"uid"`
		NeedP2P     int   `json:"need_p2p"`
		IsHidden    bool  `json:"is_hidden"`
		IsLocked    bool  `json:"is_locked"`
		IsPortrait  bool  `json:"is_portrait"`
		LiveStatus  int   `json:"live_status"`
		HiddenTill  int   `json:"hidden_till"`
		LockTill    int   `json:"lock_till"`
		Encrypted   bool  `json:"encrypted"`
		PwdVerified bool  `json:"pwd_verified"`
		LiveTime    int64 `json:"live_time"`
		RoomShield  int   `json:"room_shield"`
		IsSp        int   `json:"is_sp"`
		SpecialType int   `json:"special_type"`
	} `json:"data"`
}

// DanmuInfo
// api https://api.live.bilibili.com/xlive/web-room/v1/index/getDanmuInfo?id={}&type=0 response
type DanmuInfo struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Ttl     int    `json:"ttl"`
	Data    struct {
		Group            string  `json:"group"`
		BusinessId       int     `json:"business_id"`
		RefreshRowFactor float64 `json:"refresh_row_factor"`
		RefreshRate      int     `json:"refresh_rate"`
		MaxDelay         int     `json:"max_delay"`
		Token            string  `json:"token"`
		HostList         []struct {
			Host    string `json:"host"`
			Port    int    `json:"port"`
			WssPort int    `json:"wss_port"`
			WsPort  int    `json:"ws_port"`
		} `json:"host_list"`
	} `json:"data"`
}

func GetUid(cookie string) (int, error) {
	headers := &http.Header{}
	headers.Set("Cookie", cookie)
	headers.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:137.0) Gecko/20100101 Firefox/137.0")
	resp, err := HttpGet("https://api.bilibili.com/x/web-interface/nav", headers)
	if err != nil {
		return 0, err
	}
	j := gjson.ParseBytes(resp)
	if j.Get("code").Int() != 0 || !j.Get("data.isLogin").Bool() {
		return 0, errors.New(j.Get("message").String())
	}
	return int(j.Get("data.mid").Int()), nil
}

func GetDanmuInfo(roomID int, headers *http.Header) (*DanmuInfo, error) {
	result := &DanmuInfo{}

	signedUrl, err := WbiKeysSignString(fmt.Sprintf("https://api.live.bilibili.com/xlive/web-room/v1/index/getDanmuInfo?id=%d&type=0", roomID))
	if err != nil {
		return nil, err
	}

	err = GetJsonWithHeader(signedUrl, headers, result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func GetRoomInfo(roomID int, headers *http.Header) (*RoomInfo, error) {
	result := &RoomInfo{}

	err := GetJsonWithHeader(fmt.Sprintf("https://api.live.bilibili.com/room/v1/Room/room_init?id=%d", roomID), headers, result)
	if err != nil {
		return nil, err
	}
	return result, nil
}
