PYTHON SSE RECORD — 2026-09-04
server: hermes-webui-personal (python) http://127.0.0.1:8787
gateway: 8642

TURN 1 (date) sid=824ea77e41e7 stream=c13972...
[14.7s] context_status prefill error (caveman_prefill.txt missing -> skip)
[34.0s] tool.started terminal  date
[34.1s] metering
[34.3s] tool.completed preview date output
[37.2s] token burst ("Hari Jumat, 4 September 2026, jam 12:41 WIB.")
[37.5s] done (session snapshot)
[41.5s] title_status llm + title

TURN 2 (whoami+pwd) stream=452dbea...
[0.3s]  context_status prefill error
[2.4s]  tool.started terminal  whoami; echo ---; pwd
[2.5s]  metering
[2.6s]  tool.completed preview "adityahimawan
---
/Users/adityahimawan/work..."
[16.1s] reasoning {text:"User"}
[16.1s] metering
[16.1s] reasoning {text:" pokemon... no. Vice president. Whatever."}
[16.1s] token burst
[16.5s] done (session snapshot)
[16.5s] metering tps
[16.5s] stream_end

KEY INSIGHT:
- Python forwards REAL model reasoning as incremental 'reasoning' events when
  present (turn2 2x reasoning with partial text).
- Go shim currently DROPS reasoning.available entirely (added to kill flash).
  This breaks parity: Python shows thinking card when the model thinks.
- Python 'token' burst arrives together, same as Go. The "flash then gone"
  in Go was because reasoning.available echoed the answer text right before
  done (not real COT). Fix: shim should forward reasoning ONLY when text is
  NOT an answer echo (len>=, not backtick-wrapped, not identical to token/output).
