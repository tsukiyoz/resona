// Package teamspeak implements a TeamSpeak 3 (and compatible) client over UDP:
// handshake, EAX-encrypted packets, commands, notifications, optional voice, and file transfer helpers.
//
// Use [NewClient] with a [*crypto.Identity], call [Client.Connect], then [Client.WaitConnected]
// before sending commands or relying on client ID. Subpackages cover crypto identities,
// DNS/TSDNS resolution, transport framing, and TS3 command escaping/parsing.
//
// This is a clean-room implementation without the proprietary TeamSpeak SDK.
package teamspeak
