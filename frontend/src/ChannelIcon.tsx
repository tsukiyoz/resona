import { useState } from "react";
import { Volume2 } from "lucide-react";
import type { Channel } from "./api";

export function ChannelIcon({
  channel,
  size = 18,
}: {
  channel: Channel;
  size?: number;
}) {
  const [failedURL, setFailedURL] = useState("");
  const url = channel.iconDataURL;
  const safe =
    url.length <= 512 * 1024 &&
    /^data:image\/(?:png|gif|jpeg|webp);base64,[A-Za-z0-9+/=]+$/.test(url);
  return safe && failedURL !== url ? (
    <img
      className="channel-custom-icon"
      src={url}
      alt=""
      width={size}
      height={size}
      onError={() => setFailedURL(url)}
    />
  ) : (
    <Volume2 size={size} aria-hidden="true" />
  );
}
