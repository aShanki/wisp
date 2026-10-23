#!/usr/bin/env bash
# Fetch WhatsApp's notification sound, app icon and fonts from WhatsApp Web, for a
# personal install only (never commit them). Usage: fetch-whatsapp-assets.sh [dir]
# Writes notification.<ext>, wisp.svg (or wisp.png) and, when found, the chat
# doodle tiles wallpaper-light.png and wallpaper-dark.png to dir (build/whatsapp).
set -euo pipefail
out=${1:-"$(cd "$(dirname "$0")/.." && pwd)/build/whatsapp"}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

ua='Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36'
get() { # url file [sec-fetch-dest]
  curl -fsSL --compressed --max-time 60 -A "$ua" -H 'Accept-Language: en-US,en;q=0.9' \
    -H "Sec-Fetch-Dest: ${3:-empty}" -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Site: none' \
    -o "$2" "$1"
}
unescape() { sed 's|\\/|/|g'; }

get https://web.whatsapp.com/ "$tmp/index.html" document

# The module WAWebNotificationTone plays bx("<id>"); the page maps ids to URIs.
id=
while read -r js; do
  get "$js" "$tmp/b.js"
  id=$(grep -o '__d("WAWebNotificationTone"[^;]*' "$tmp/b.js" | grep -oE '"bx"\)\("[0-9]+"\)' | grep -oE '[0-9]+' | head -1) || true
  [[ -n $id ]] && break
done < <(grep -oE 'https://static\.whatsapp\.net/rsrc\.php/[^"]+\.js' "$tmp/index.html" | sort -u)
[[ -n $id ]] || { echo "notification tone not found in WhatsApp Web" >&2; exit 1; }
sound=$(grep -hoE "\"$id\":\{\"uri\":\"[^\"]+\"" "$tmp/index.html" "$tmp/b.js" | head -1 | cut -d'"' -f6 | unescape)
[[ $sound == https://static.whatsapp.net/* ]] || { echo "no URI for asset $id" >&2; exit 1; }
get "$sound" "$tmp/notification.${sound##*.}"
file -b "$tmp"/notification.* | grep -qiE 'audio|ogg' || { echo "$sound is not audio" >&2; exit 1; }

# The app icon from the web app manifest: its SVG, else the largest bitmap.
get https://web.whatsapp.com/data/manifest.json "$tmp/manifest.json" manifest
icon=$(jq -r '[.icons[] | select(.src | endswith(".svg"))][0].src // empty' "$tmp/manifest.json")
if [[ -n $icon ]]; then
  get "$icon" "$tmp/wisp.svg"
  file -b "$tmp/wisp.svg" | grep -q SVG || { echo "$icon is not SVG" >&2; exit 1; }
  # Dark-mode variant: the logo on WhatsApp's dark panel colour instead of white.
  sed -Ei 's/(<circle[^>]*fill=")white"/\1#111B21"/' "$tmp/wisp.svg"
else
  icon=$(jq -r '.icons | max_by(.sizes | split("x")[0] | tonumber).src' "$tmp/manifest.json")
  get "$icon" "$tmp/icon"
  ffmpeg -v error -y -i "$tmp/icon" "$tmp/wisp.png"
fi

# The chat doodle tiles: a bundle names them bg-chat-{light,dark}-refreshed next
# to their bx ids. Each is an alpha mask; it is drawn here in WhatsApp's ink on
# its wallpaper colour (WDS-systems-chat-foreground/background-wallpaper), so
# Wisp tiles one opaque image. Optional: without them Wisp draws the plain colour.
walls=
while read -r js; do
  get "$js" "$tmp/w.js"
  grep -q 'bg-chat-light-refreshed' "$tmp/w.js" && { walls=1; break; }
done < <(grep -oE 'https://static\.whatsapp\.net/rsrc\.php/[^"]+\.js' "$tmp/index.html" | sort -u)
declare -A paper=([light]="F5F1EB r=234:g=224:b=211" [dark]="161717 r=255:g=255:b=255:a=val*0.1")
for mode in light dark; do
  [[ -n $walls ]] || { echo "chat wallpaper not found in WhatsApp Web" >&2; break; }
  read -r bg ink <<<"${paper[$mode]}"
  wid=$(grep -oE "id:\"bg-chat-$mode-refreshed\",[^}]*\(\"[0-9]+\"" "$tmp/w.js" | grep -oE '[0-9]+"$' | tr -d '"' | head -1) || true
  wall=$(grep -hoE "\"$wid\":\{\"uri\":\"[^\"]+\"" "$tmp/index.html" "$tmp/w.js" | head -1 | cut -d'"' -f6 | unescape) || true
  if [[ -n $wid && $wall == https://static.whatsapp.net/* ]] && get "$wall" "$tmp/wall" \
      && size=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=s=x:p=0 "$tmp/wall") \
      && ffmpeg -v error -y -i "$tmp/wall" -frames:v 1 -filter_complex \
        "[0]format=rgba,lutrgb=${ink}[ink];color=c=0x${bg}:s=${size}[bg];[bg][ink]overlay=shortest=1,format=rgb24" \
        "$tmp/wallpaper-$mode.png"; then
    echo "$wall -> $out/wallpaper-$mode.png"
  else
    echo "chat wallpaper ($mode) not found in WhatsApp Web" >&2
  fi
done

css=$(grep -oE 'https://static\.whatsapp\.net/rsrc\.php/[^" ]+\.css' "$tmp/index.html" | head -1) || true
if [[ -n $css ]] && get "$css" "$tmp/style.css" style; then
  for style in normal italic; do
    font=$(grep -oE "@font-face\{font-family:Roboto Variable;font-style:$style;[^}]+" "$tmp/style.css" \
      | grep -oE 'url\([^)]+\)' | head -1 | sed 's/^url(//;s/)$//') || true
    [[ -n $font ]] || continue
    [[ $font == https://* ]] || font="https://static.whatsapp.net$font"
    name=Roboto.woff2
    [[ $style != italic ]] || name=Roboto-Italic.woff2
    if ! get "$font" "$tmp/$name" font; then
      rm -f "$tmp/$name"
    fi
  done
fi

mkdir -p "$out"
rm -f "$out"/notification.* "$out"/wisp.* "$out"/wallpaper-*
mv "$tmp"/notification.* "$tmp"/wisp.* "$out"/
mv "$tmp"/wallpaper-*.png "$out"/ 2>/dev/null || true
for font in "$tmp"/Roboto*.woff2; do
  [[ ! -s $font ]] || mv "$font" "$out/"
done
echo "$sound -> $(ls "$out"/notification.*)"
echo "$icon -> $(ls "$out"/wisp.*)"
