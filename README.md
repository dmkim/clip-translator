# clip-translator

클립보드의 텍스트를 Gemini 또는 로컬 LLM으로 변환해 다시 클립보드에 덮어쓰는 초경량 Windows 데스크톱 유틸리티입니다. 프리셋 프롬프트를 활용하면 맞춤법 검사기, 번역기, 요약기, 영작 도우미 등 게임 채팅(WoW 북미 서버)부터 업무 메신저까지 다양한 용도로 활용할 수 있습니다.

## 다운로드

**[최신 clip-translator.exe 다운로드](https://github.com/dmkim/clip-translator/releases/latest/download/clip-translator.exe)**

> 이 앱은 코드 서명이 없는 개인 배포 빌드라 Windows SmartScreen 경고(예: "Windows의 PC 보호")가 표시될 수 있습니다. 소스 코드가 공개되어 있으니 안심하고 **[추가 정보] → [실행]**을 선택해 실행하면 됩니다.
>
> 무결성 확인이 필요하면 [checksums.txt](https://github.com/dmkim/clip-translator/releases/latest/download/checksums.txt)의 SHA256과 내려받은 파일을 비교하세요.

## 워크플로우

1. 텍스트를 선택한 뒤 `Ctrl + X` 또는 `Ctrl + C`
2. 글로벌 단축키(기본 `Ctrl + Shift + T`) 입력
3. 현재 선택된 프리셋(시스템 프롬프트)과 AI 제공자(Gemini 또는 로컬 LLM)로 변환 요청
4. 변환 결과를 클립보드에 덮어쓰고 비프음과 함께 창에 결과 표시
5. `Ctrl + V`로 붙여넣기

## 요구 사항

- Windows 10/11 (amd64)
- Go 1.24+ (빌드 시, C 컴파일러 불필요)
- Google Gemini API Key ([Google AI Studio](https://aistudio.google.com/apikey)에서 무료로 발급 가능) — Gemini 사용 시
- OpenAI 호환 로컬 LLM 서버(Ollama, LM Studio, llama.cpp 등) — 로컬 LLM 사용 시

## 빌드

```powershell
# 저장소 루트에서 실행
./build/package/build.ps1
```

버전을 주입하려면 `-Version`을 지정합니다. `dev`(기본값)로 빌드하면 자동 업데이트 확인을 건너뜁니다.

```powershell
./build/package/build.ps1 -Version v0.1.0
```

스크립트는 다음을 수행합니다.

1. `rsrc`가 없으면 `go install github.com/akavel/rsrc@latest`로 설치
2. `build/package/clip-translator.exe.manifest`를 `cmd/clip-translator/rsrc.syso`로 컴파일
3. `-H=windowsgui -s -w`로 콘솔 없는 단일 실행 파일 `bin/clip-translator.exe` 생성

수동 빌드:

```powershell
rsrc -manifest build/package/clip-translator.exe.manifest -o cmd/clip-translator/rsrc.syso
go build -ldflags="-H=windowsgui -s -w" -o bin/clip-translator.exe ./cmd/clip-translator
```

## 사용법

1. `bin/clip-translator.exe` 실행
2. 하단 **AI 설정**에서 제공자를 선택합니다.
   - **Gemini**: API Key 입력 후 **[키 저장]**
   - **로컬 LLM**: **[설정...]**에서 Base URL/모델/타임아웃을 저장하고 **[테스트]**로 연결 확인
3. **프리셋** 드롭다운에서 프롬프트 선택/편집 후 **[프롬프트 저장]**
4. 상태 표시 옆 **⚙ 아이콘**을 눌러 글로벌 단축키 변경 가능 (예: `Ctrl+Shift+F9`)
5. 아무 프로그램에서 텍스트를 복사/잘라내기 후 설정된 단축키 입력
6. 번역 시작 시 두 음(523→659Hz)의 시작음이 울리고, 성공 시 `800Hz` 완료음과 함께 `결과(OUT)`이 갱신되어 클립보드에 복사됩니다
   - 실패 시 `300Hz` 경고음과 오류 메시지 표시

## UI

- 네이티브 Win32(walk) 기반의 컴팩트한 단일 창
- 상단 상태 라벨(🟢 대기 / 🟡 처리 중 / 🔴 오류) + **⚙ 아이콘**(단축키 변경) + **항상 위** 체크박스
- 프리셋 선택/추가/삭제, 프롬프트 편집, 최근 결과(재복사), AI 제공자/API 키/로컬 LLM 설정
- 단축키 변경 시 기존 등록을 해제하고 즉시 재등록 (실패 시 기존 단축키 복원)
- 앱을 다시 실행하면 새 창을 띄우지 않고 기존 창을 활성화
- 앱 시작 시 트레이 아이콘 표시, X는 트레이로 숨김, 트레이 우클릭 → **[종료]**
- 창 크기 `380 × 540` (최소 높이 500, DPI 자동 스케일)
- 알림음: 시작(523→659Hz 상승 두 음) / 성공(800Hz) / 실패(300Hz)로 구분

## 설정 파일

설정은 실행 파일과 같은 폴더의 `config.json`에 저장됩니다. `go run`으로 실행하는 경우(실행 파일이 임시 폴더에 생성됨)에는 `%AppData%\clip-translator\config.json`을 사용합니다.

`config.json`이 없으면 실행 시 `internal/config/default.json`에 내장된 프리셋으로 자동 생성되며, **API Key는 Gemini와 로컬 LLM 모두 빈 값**으로 저장됩니다. 템플릿은 `configs/config.example.json`에서도 확인할 수 있습니다.

| 필드 | 설명 | 기본값 |
| --- | --- | --- |
| `api_key` | Gemini API Key (저장소에 커밋 금지) | `""` |
| `model` | 사용할 Gemini 모델 | `gemini-flash-lite-latest` |
| `provider` | AI 제공자 (`gemini` 또는 `local`) | `gemini` |
| `local_base_url` | OpenAI 호환 로컬 서버 주소 | `http://localhost:11434/v1` |
| `local_model` | 로컬 LLM 모델 이름 | `""` |
| `local_api_key` | 로컬 서버 인증 키 (선택) | `""` |
| `local_timeout_seconds` | 로컬 LLM 요청 타임아웃 | `120` |
| `hotkey` | 글로벌 단축키 | `Ctrl+Shift+T` |
| `always_on_top` | 항상 위 표시 | `false` |
| `active_profile` | 현재 선택된 프리셋 이름 | `WoW 북미 서버 채팅` |
| `profiles` | 이름과 시스템 프롬프트 목록 | 5개 기본 프리셋 |

단축키는 `Ctrl`, `Alt`, `Shift`, `Win` 조합과 `A-Z`, `0-9`, `F1-F20`, `Space`, `Tab`, `Enter`, `Esc`, `Delete`, 방향키를 지원합니다. 이전 기본 모델(`gemini-2.5-flash-lite`, `gemini-3.5-flash-lite`)이 설정되어 있으면 실행 시 `gemini-flash-lite-latest`로 자동 마이그레이션됩니다. 이 마이그레이션은 Gemini 모델에만 적용되며 로컬 모델 이름은 그대로 유지됩니다.

응답 지연을 줄이기 위해 `temperature`는 0.3으로 낮추고 내부 추론을 최소화합니다. Flash-Lite 계열(Gemini 3.x, `-latest`)은 `thinkingLevel: minimal`, Gemini 2.5 Flash는 `thinkingBudget: 0`을 사용하며, 그 외 모델에는 추론 옵션을 보내지 않습니다. 또한 Gemini 사용 시 앱 시작 시 엔드포인트에 미리 연결하고, 5분마다 연결을 갱신하며 최대 30분간 커넥션을 유지해 첫 요청·재연결 지연을 줄입니다.

## 로컬 LLM

**AI 설정 → 제공자**에서 `로컬 LLM (OpenAI 호환)`을 선택한 뒤 **[설정...]**을 누르면 두 가지 방법 중 하나를 고를 수 있습니다.

- **Ollama 자동 설치 (권장)**: winget으로 Ollama를 설치하고 GPU VRAM에 따라 `gemma3:4b` 또는 `gemma3:1b`를 자동 선택
- **직접 설정**: 이미 설치된 LM Studio/Ollama/llama.cpp 또는 원격 서버의 Base URL과 모델을 직접 입력

OpenAI 호환 `/v1/chat/completions` API를 지원하는 서버라면 별도 구현 없이 사용할 수 있습니다.

| 런타임 | Base URL | 예시 |
| --- | --- | --- |
| Ollama | `http://localhost:11434/v1` | `ollama serve` |
| LM Studio | `http://localhost:1234/v1` | Local Server 활성화 |
| llama.cpp | `http://localhost:8080/v1` | `llama-server -m model.gguf --port 8080` |

### Windows 자동 설치 (winget)

**[설정...] → Ollama 자동 설치 (권장)**을 선택하면 winget으로 Ollama를 설치하고 모델을 내려받아 자동 설정합니다. 모델 다운로드 후 실제 **워밍업 요청**까지 성공해야 자동 설치가 완료됩니다. winget(App Installer)이 없거나 설치 권한이 없으면 수동 설치가 필요합니다.

모델 자동 선택 기준:

- NVIDIA VRAM 6GB 이상: `gemma3:4b`
- NVIDIA VRAM 6GB 미만, AMD/Intel, 감지 실패: `gemma3:1b`

자동 설치 화면에서 **자동 (권장) / gemma3:4b / gemma3:1b**를 직접 선택할 수도 있습니다. VRAM이 부족한 환경에서 4b를 강제하면 경고가 표시됩니다.

```powershell
ollama serve
ollama pull gemma3:4b
# 또는
ollama pull gemma3:1b
```

- **[불러오기]**를 누르면 `GET {base_url}/models`에서 모델 목록을 가져와 선택할 수 있습니다.
- 모델 이름은 서버에 설치된 모델과 정확히 일치해야 합니다.
- 원격 서버나 인증이 필요한 서버는 **API Key**를 입력하면 `Authorization: Bearer <key>` 헤더로 전송됩니다.
- **[테스트]**는 `GET {base_url}/models`로 연결을 확인합니다. 일부 서버가 이 엔드포인트를 지원하지 않으면 경고가 표시되지만 번역은 동작할 수 있습니다.
- 로컬 LLM은 프리셋과 입력을 하나의 user 메시지로 병합해 전달합니다. Ollama/Gemma의 system 처리 차이를 보완합니다.
- 기존 기본 WoW 프리셋은 감사/약어 예시가 포함된 새 프롬프트로 자동 업데이트됩니다. 사용자가 수정한 프롬프트는 유지됩니다.
- 로컬 LLM은 스트리밍을 사용하지 않으며, 응답이 완료될 때까지 기다립니다. 기본 타임아웃은 120초입니다.
- Ollama 모델은 앱 실행 중 메모리에 유지되며, 앱을 종료하면 모델을 내립니다. 앱이 직접 Ollama 서버를 시작한 경우 서버도 함께 종료합니다.

## 자동 업데이트

앱은 시작 3초 후 GitHub Releases를 확인하고, 새 버전이 있으면 릴리스 노트와 함께 업데이트 여부를 묻습니다. 승인하면 실행 파일을 내려받아 SHA256 체크섬을 검증한 뒤 교체하고 자동으로 재시작합니다.

- 저장소: `dmkim/clip-translator`
- 확인 주소: GitHub Releases의 최신 정식 릴리스 (draft/pre-release 제외)
- 개발 빌드(`-Version` 미지정, `dev`)는 확인을 건너뜁니다
- `config.json`은 그대로 유지됩니다
- Program Files처럼 쓰기 권한이 없는 위치에서는 교체가 실패할 수 있으며, 이 경우 안내 메시지와 함께 릴리스 페이지 링크를 보여줍니다

### 릴리스 절차

1. `v0.1.0` 형식의 태그를 push합니다.
2. GitHub Actions(`.github/workflows/release.yml`)가 Windows 바이너리를 빌드하고 `checksums.txt`와 함께 릴리스에 업로드합니다.
3. 태그가 버전으로 주입되므로 이후 실행부터 자동 업데이트 대상이 됩니다.

```powershell
git tag v0.1.0
git push origin v0.1.0
```

릴리스 자산은 다음 두 파일이어야 합니다.

| 자산 | 설명 |
| --- | --- |
| `clip-translator.exe` | `-H=windowsgui`로 빌드된 단일 실행 파일 |
| `checksums.txt` | `sha256sum` 형식(`<hash>  <filename>`)의 체크섬 |

## 프로젝트 구조

```text
clip-translator/
├── cmd/clip-translator/main.go      # 진입점, 의존성 조립 및 GUI 실행
├── internal/config/                 # config.json 로드/저장, default.json 프리셋 템플릿
├── internal/ai/translator.go        # 번역 제공자 인터페이스
├── internal/ai/gemini.go            # Gemini REST 클라이언트 (30초 타임아웃)
├── internal/ai/local.go             # OpenAI 호환 로컬 LLM 클라이언트 (기본 120초 타임아웃)
├── internal/clipboard/watcher.go    # 글로벌 단축키 + 클립보드 파이프라인
├── internal/ui/window.go            # walk GUI, 상태 표시, 비프음
├── internal/ui/assets/              # 앱 아이콘/단축키 아이콘 (임베드)
├── internal/version/version.go      # 빌드 시 주입되는 버전
├── internal/update/update.go        # GitHub Releases 기반 자동 업데이트
├── configs/config.example.json      # 설정 템플릿
├── .github/workflows/release.yml    # 태그 push 시 릴리스 빌드/업로드
└── build/package/                   # 매니페스트, 앱 아이콘(.ico), 빌드 스크립트
```

## 보안

`config.json`에는 Gemini API Key와 로컬 LLM API Key가 평문으로 저장되며 `.gitignore`에 등록되어 있습니다. 실행 파일을 공유할 때 `config.json`을 함께 배포하지 마세요.
