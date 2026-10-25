# SnapSloth 🦥

> For every screenshot you said "baad mai" to.

Your screenshots folder is full of fee notices, payment requests, tickets, OTPs and memes, all named
`Screenshot from 2026-08-30 23-51-07.png`. You can't search them, you forget the important ones, and
some of them (OTPs, Aadhaar, bank details) shouldn't be lying around at all.

SnapSloth reads every screenshot with a **local AI model** while you sleep, then:

- 🗂️ **renames and sorts** them into folders (`sorted/college_notice/2026-10-20_hostel-fee-due-notice.png`)
- 🔎 makes them **searchable by the text inside them**
- ⚠️ **flags risky ones** (OTPs, bank/UPI details, Aadhaar/PAN, passwords), with values masked
- ☕ gives you a **morning report**, including the things you saved and forgot

**Nothing leaves your laptop.** It uses Gemma 4 (open model) through Ollama, so it works with Wi-Fi off.

## Demo

📹 *Video coming soon*

<!-- ![SnapSloth](docs/ui.png) -->

## Setup (5 minutes + model download)

```bash
# 1. Ollama, the local model runner
curl -fsSL https://ollama.com/install.sh | sh
ollama pull gemma4:e4b          # ~5.5 GB. Slow laptop? use gemma4:e2b

# 2. SnapSloth (needs Go 1.27+)
go install github.com/vimla01/snapsloth@latest

# 3. Open the app
snapsloth ui
```

`snapsloth ui` opens the app in your browser. Pick your screenshots folder, press **Start scan**,
then **Sort**. That's it. (Try it on a copy first: `cp -r ~/Pictures/Screenshots ~/ss-test`.)

The app only listens on `127.0.0.1`, so no other device can reach it.

## Lazy mode: every night at 2 AM 🌙

`crontab -e`, add this line:

```cron
0 2 * * * $HOME/go/bin/snapsloth scan $HOME/Pictures/Screenshots && $HOME/go/bin/snapsloth sort $HOME/Pictures/Screenshots; $HOME/go/bin/snapsloth report $HOME/Pictures/Screenshots > $HOME/snapsloth-morning.txt 2>&1
```

Morning: open `~/snapsloth-morning.txt` (or `snapsloth ui`) with your chai. The laptop has to be awake at 2 AM.

Phone screenshots? Sync them to your laptop with [Syncthing](https://syncthing.net/) (local, no cloud).

## Terminal commands

| command | what it does |
|---|---|
| `snapsloth ui [dir]` | the point-and-click app |
| `snapsloth scan <dir>` | read new screenshots (`--limit N`, `--model`, `--dry-run`) |
| `snapsloth sort <dir>` | rename and file into `sorted/<category>/` (`--dry-run` to preview) |
| `snapsloth undo <dir>` | put the last sort back |
| `snapsloth search <dir> "hostel fee"` | find by text, title or category |
| `snapsloth risky <dir>` | list sensitive screenshots and why |
| `snapsloth report <dir>` | morning summary |
| `snapsloth eval <dir> labels.csv` | accuracy vs your own labels (`--init` makes a blank CSV) |

Scans are resumable: Ctrl+C anytime, and already-read screenshots are skipped next time.

## Results

Measured on my laptop: Intel i5-1334U, 16 GB RAM, **CPU only, no GPU**, `gemma4:e4b`:

| | |
|---|---|
| Speed | **~70–80 s per screenshot** |
| My 367-screenshot backlog | ~7–8 hours, i.e. **one night** |
| A normal night (~10 new) | ~13 minutes |
| Accuracy | _TBD_ % (`snapsloth eval`) |
| Your time per night | 0 minutes |

Two tricks made it ~3× faster: screenshots are shrunk to 1024 px before the model sees them
(a full-HD screenshot is 932 image tokens; at 1024 px it's 264, and text is still readable;
at 768 px the model starts guessing), and the instructions go in the system prompt so Ollama
caches them instead of re-reading them for every image.

Close heavy apps before a big first run: when RAM runs out and the laptop swaps, it gets ~3× slower.

## Privacy

- 100% local: the only network call is to Ollama on your own machine.
- The model's answer is double-checked with regex (OTP, Aadhaar, PAN, UPI, card numbers, passwords),
  and every value is **masked** (`XXXX-XXXX-1234`) on screen *and* in the saved index.
- **Never deletes anything.** Sort only renames, undo puts it back, risky only suggests.
- Its data lives in `<your folder>/.snapsloth/`, never in this repo.

## Limitations

- Slow on CPU (~75 s per screenshot). That's why it runs at night.
- The model sometimes picks the wrong category, and OCR struggles with tiny text and Hindi/Marathi.
- "Things you forgot" is a keyword guess, not real deadline understanding.
- Identical duplicate files are only sorted once.

## License

MIT
