# [개발 지시서] 범용 클립보드 AI 번역기 (`clip-translator`)

## 1. 프로젝트 개요

* **목적:** 게임(WoW 북미 서버 채팅), 업무 메신저(Slack/이메일), 문서 요약, 문법 교정 등 어떤 환경에서든 텍스트를 복사/잘라내기(`Ctrl+X` / `Ctrl+C`)한 뒤 글로벌 단축키를 누르면 **선택된 프리셋(시스템 프롬프트)에 따라 AI가 변환한 결과를 클립보드에 덮어쓰는** 초경량 Windows 데스크톱 유틸리티 개발.

* **핵심 워크플로우:**
  1. 사용자가 입력창이나 문서에서 텍스트를 `Ctrl + X` (또는 `Ctrl + C`)
  2. 글로벌 단축키(기본값: `Ctrl + Alt + T`) 입력
  3. 백그라운드 리스너가 클립보드 텍스트와 **현재 활성화된 프리셋 프롬프트**를 조합해 Gemini API 호출
  4. 변환된 결과를 클립보드에 덮어쓰고 완료 비프음 출력 + 소형 GUI 창에 결과 업데이트
  5. 사용자가 `Ctrl + V`로 즉시 붙여넣기

## 2. 디렉토리 구조 (`golang-standards/project-layout` 준수)

```text
clip-translator/
├── cmd/
│   └── clip-translator/
│       └── main.go                         # 애플리케이션 진입점, 의존성 조립 및 GUI 실행
├── internal/
│   ├── config/
│   │   ├── config.go                       # 설정(API Key, 단축키, AlwaysOnTop, 프리셋 목록) JSON 로드/저장
│   │   └── defaults.go                     # 최초 실행 시 생성할 기본 프리셋(WoW 채팅, 비즈니스 영작, 문법 교정, 요약 등)
│   ├── ai/
│   │   └── gemini.go                       # Gemini REST API 클라이언트 (동적 System Instruction 주입, 타임아웃 처리)
│   ├── clipboard/
│   │   └── watcher.go                      # 글로벌 단축키 등록(RegisterHotKey) 및 클립보드 I/O 파이프라인
│   └── ui/
│       └── window.go                       # 심플 소형 GUI 윈도우, 프리셋 선택/프롬프트 편집기, 상태 표시, 비프음 제어
├── configs/
│   └── config.example.json                 # 기본 설정 및 프리셋 템플릿 파일
├── build/
│   └── package/
│       ├── clip-translator.exe.manifest    # Windows Common Controls v6 매니페스트 (네이티브 UI 스타일용)
│       └── build.ps1                       # Windows GUI 단일 바이너리 빌드 스크립트
├── .gitignore
├── go.mod
├── go.sum
└── README.md
```

## 3. 기술 스택 및 권장 패키지

* **언어:** Go 1.24+ (Windows 타겟, 순수 Go 구현으로 CGO 의존성 최소화)
* **GUI 라이브러리:** `github.com/tailscale/walk` (순수 Win32 API 기반 네이티브 Windows GUI, 가볍고 단일 `.exe` 패키징 용이)
* **단축키 & 클립보드:**
  * `golang.design/x/hotkey` (글로벌 단축키 감지)
  * `golang.design/x/clipboard` (Windows 클립보드 읽기/쓰기)
* **AI 모델:** Google Gemini API (`gemini-2.5-flash-lite` 또는 `gemini-2.5-flash` — 빠른 응답 속도 및 무료 티어 활용)

## 4. 데이터 모델 (`config.json` 구조)

사용자가 GUI에서 수정한 프리셋과 설정이 실행 파일 경로의 `config.json`에 영구 저장되도록 구성한다.

```json
{
  "api_key": "",
  "always_on_top": true,
  "active_profile": "WoW 북미 서버 채팅",
  "profiles": [
    {
      "name": "WoW 북미 서버 채팅",
      "prompt": "You are an expert English localization assistant specialized in World of Warcraft (NA realms). Translate the Korean input into natural, native-style English that NA WoW players actually use in-game. Use common shorthand/slang (np, ty, mb, afk, brb, pull, cc, kick, int, dispell, oom, lol, rip, gg). Keep it casual, punchy, lowercase preference, minimal punctuation. Output ONLY the single best translated sentence ready to paste into chat."
    },
    {
      "name": "비즈니스/슬랙 영작",
      "prompt": "Translate the user's Korean input into polite, natural, and concise professional English suitable for Slack or workplace emails. Output ONLY the translated English text without explanations."
    },
    {
      "name": "영어 문법/네이티브 교정",
      "prompt": "Proofread and rewrite the user's English text to sound natural and native while preserving the original meaning and tone. Output ONLY the corrected English text."
    },
    {
      "name": "핵심 3줄 요약 (한국어)",
      "prompt": "Summarize the provided text into 3 concise bullet points in Korean. Focus only on key takeaways. Output ONLY the summary."
    }
  ]
}
```

## 5. 기능 요구사항 (Functional Requirements)

### F1. 심플 소형 GUI 창 (`internal/ui/window.go`)

* **창 크기:** 가로 `380px` × 세로 `430px` (컴팩트한 크기 유지, 최소화 버튼 지원)
* **UI 구성 요소 (위에서 아래로 배치):**
  1. **상단 상태바 & 옵션:**
     * 현재 상태 라벨: `🟢 대기 중 (Ctrl+Alt+T)` / `🟡 처리 중...` / `🔴 오류 발생`
     * 체크박스: `항상 위 (Always on Top)` (체크 시 `SetWindowPos`의 `HWND_TOPMOST` 즉시 반영)
  2. **프리셋 선택 & 프롬프트 편집 영역:**
     * 프리셋 드롭다운(`ComboBox`) + `[+ 추가]` / `[삭제]` 버튼
     * 멀티라인 `TextEdit`: 현재 선택된 프리셋의 시스템 프롬프트 내용을 표시하며 사용자가 직접 타이핑하여 수정 가능
     * `[프롬프트 저장]` 버튼 (클릭 시 현재 수정된 프롬프트 내용을 `config.json`에 저장하고 즉시 반영)
  3. **최근 실행 결과 뷰어 (Read-Only):**
     * `입력 (IN):` 마지막으로 클립보드에서 읽어온 텍스트 (1줄 요약 표시)
     * `결과 (OUT):` AI가 반환한 변환 결과 (강조 표시 및 `[다시 복사]` 버튼 포함)
  4. **하단 API 설정 영역:**
     * Gemini API Key 입력 필드 (`PasswordMode` 마스킹) + `[키 저장]` 버튼

### F2. 글로벌 단축키 및 클립보드 파이프라인 (`internal/clipboard/watcher.go`)

* `Ctrl + Alt + T` 입력 시 비동기 고루틴으로 실행 (UI 스레드 블로킹 방지).
* 실행 시점의 `active_profile` 프롬프트를 동적으로 읽어 API에 전달.
* 클립보드 내용이 비어있거나 공백만 있는 경우 API를 호출하지 않고 무시.
* 처리 진행 중 중복 단축키 입력이 들어오면 무시(Debounce/Mutex 처리).

### F3. Gemini API 연동 (`internal/ai/gemini.go`)

* `net/http`를 사용해 REST API 호출 (`Timeout: 8초` — 요약 등 긴 텍스트 처리를 고려).
* `system_instruction` 필드에 현재 선택된 프리셋의 `prompt`를 삽입하고, `contents`에 클립보드 텍스트를 넣어 요청.

### F4. 오디오 피드백 (`internal/ui/window.go`)

* `kernel32.dll`의 `Beep` Win32 API를 `syscall`로 호출.
* **변환 성공 & 클립보드 덮어쓰기 완료 시:** 경쾌한 고음 비프음 (`800Hz`, `120ms`)
* **변환 실패(네트워크/API 키 오류) 시:** 낮은 경고음 (`300Hz`, `250ms`)

## 6. 빌드 및 실행 요구사항 (`build/package/build.ps1`)

1. `rsrc` 도구를 이용해 `clip-translator.exe.manifest`를 리소스(`.syso`)로 컴파일하여 최신 Windows 테마가 적용된 UI가 렌더링되도록 구성.
2. 콘솔 창이 뜨지 않도록 `-ldflags="-H=windowsgui"` 옵션을 포함하여 단일 `.exe` 파일로 빌드.

```powershell
# 빌드 예시 명령어
rsrc -manifest build/package/clip-translator.exe.manifest -o cmd/clip-translator/rsrc.syso
go build -ldflags="-H=windowsgui -s -w" -o bin/clip-translator.exe ./cmd/clip-translator
```

## 7. 구현 시 주의사항

* **스레드 안전성:** Win32 GUI(`walk`)의 UI 요소 업데이트(상태 라벨 변경, 결과 텍스트 갱신)는 반드시 `mw.Synchronize(func() { ... })`를 통해 메인 UI 스레드에서 실행해야 한다.
* **보안:** API Key가 포함된 `config.json`은 `.gitignore`에 등록하고, 저장소에는 `configs/config.example.json`만 커밋한다.