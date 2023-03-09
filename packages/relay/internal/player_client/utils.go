package player_client

import "github.com/pion/webrtc/v3"

func parseWebrtcConfiguration(v any) (conf webrtc.Configuration) {
	config, ok := v.(map[string]any)
	if !ok {
		return
	}
	if v, ok := config["iceServers"]; ok {
		if servers, ok := v.([]interface{}); ok {
			for _, serverV := range servers {
				if server, ok := serverV.(map[string]interface{}); ok {
					var iceServer webrtc.ICEServer
					url, ok := server["urls"]
					if _, isString := url.(string); !ok || !isString {
						continue
					}
					iceServer.URLs = append(iceServer.URLs, url.(string))
					username, usernameOk := server["username"]
					credential, credentialOk := server["credential"]
					if _, isString := username.(string); usernameOk && isString {
						iceServer.Username = username.(string)
					}
					if _, isString := credential.(string); credentialOk && isString {
						iceServer.Credential = credential
					}
					conf.ICEServers = append(conf.ICEServers, iceServer)
				}
			}
		}
	}
	return webrtc.Configuration{}
}
