import { useEffect, useState } from "react";
import { Volume2 } from "lucide-react";
import { api, type Channel } from "./api";

const resources = new Map<string, Promise<string>>();
const resourceBytes = new Map<string, number>();
let bytes = 0;
let resourceSession = "";
let active = 0;
const queue: {run: () => void; cancel: () => void}[] = [];
function loadResource(sessionID: string, ref: string): Promise<string> {
  if (resourceSession !== sessionID) {
    for (const job of queue.splice(0)) job.cancel();
    resources.clear(); resourceBytes.clear(); bytes = 0; resourceSession = sessionID;
  }
  const existing = resources.get(ref);
  if (existing) return existing;
  if (active >= 3 && queue.length >= 128) return Promise.resolve("");
  const promise = new Promise<string>((resolve) => {
    const run = () => {
      if (resourceSession !== sessionID) { resolve(""); queue.shift()?.run(); return; }
      active++;
      api.GetIconResource(sessionID, ref).then(resource => {
        const url = resource.ref === ref && resource.dataURL.length <= 350000 ? resource.dataURL : "";
        if (resourceSession === sessionID && resources.has(ref)) {
          resourceBytes.set(ref, url.length * 2); bytes += url.length * 2;
          while (bytes > 8 * 1024 * 1024 && resources.size) {
            const oldest = resources.keys().next().value!;
            bytes -= resourceBytes.get(oldest) ?? 0;
            resourceBytes.delete(oldest); resources.delete(oldest);
          }
        }
        resolve(url);
      }, () => resolve("")).finally(() => { active--; queue.shift()?.run(); });
    };
    if (active < 3) run(); else queue.push({run, cancel: () => resolve("")});
  });
  resources.set(ref, promise);
  if (resources.size > 128) {
    const oldest = resources.keys().next().value!;
    bytes -= resourceBytes.get(oldest) ?? 0;
    resourceBytes.delete(oldest); resources.delete(oldest);
  }
  return promise;
}

export function ChannelIcon({
  channel,
  sessionID,
  size = 18,
}: {
  channel: Channel;
  sessionID: string;
  size?: number;
}) {
  const [failedURL, setFailedURL] = useState("");
  const [resource, setResource] = useState({key: "", url: ""});
  const key = `${sessionID}:${channel.iconRef}`;
  useEffect(() => {
    let current = true;
    if (sessionID && channel.iconRef) {
      void loadResource(sessionID, channel.iconRef).then(url => { if (current) setResource({key, url}); });
    }
    return () => { current = false; };
  }, [sessionID, channel.iconRef, key]);
  const url = resource.key === key ? resource.url : "";
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
