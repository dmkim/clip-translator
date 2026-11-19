# clip-translator

클립보드의 텍스트를 Gemini로 변환해 다시 클립보드에 덮어쓰는 초경량 Windows 데스크톱 유틸리티입니다. 게임 채팅(WoW 북미 서버), 업무 메신저 영작, 문법 교정, 요약 등 어떤 환경에서도 동작합니다.

## 워크플로우

1. 텍스트를 선택한 뒤 `Ctrl + X` 또는 `Ctrl + C`
2. 글로벌 단축키(기본 `Ctrl + Shift + T`) 입력
3. 현재 선택된 프리셋(시스템 프롬프트)으로 Gemini API 호출
4. 변환 결과를 클립보드에 덮어쓰고 비프음과 함께 창에 결과 표시
5. `Ctrl + V`로 붙여넣기

## 요구 사항

- Windows 10/11 (amd64)
- Go 1.24+ (빌드 시, C 컴파일러 불필요)
- Google Gemini API Key ([Google AI Studio](https://aistudio.google.com/apikey))

## 빌드

```powershell
# 저장소 루트에서 실행
./build/package/build.ps1
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
2. 하단 **API 설정**에 Gemini API Key 입력 후 **[키 저장]**
3. **프리셋** 드롭다운에서 프롬프트 선택/편집 후 **[프롬프트 저장]**
4. 상태 표시 옆 **⚙ 아이콘**을 눌러 글로벌 단축키 변경 가능 (예: `Ctrl+Shift+F9`)
5. 아무 프로그램에서 텍스트를 복사/잘라내기 후 설정된 단축키 입력
6. 번역 시작 시 두 음(523→659Hz)의 시작음이 울리고, 성공 시 `800Hz` 완료음과 함께 `결과(OUT)`이 갱신되어 클립보드에 복사됩니다
   - 실패 시 `300Hz` 경고음과 오류 메시지 표시

## UI

- 네이티브 Win32(walk) 기반의 컴팩트한 단일 창
- 상단 상태 라벨(🟢 대기 / 🟡 처리 중 / 🔴 오류) + **⚙ 아이콘**(단축키 변경) + **항상 위** 체크박스
- 프리셋 선택/추가/삭제, 프롬프트 편집, 최근 결과(재복사), API 키 관리
- 단축키 변경 시 기존 등록을 해제하고 즉시 재등록 (실패 시 기존 단축키 복원)
- 창 크기 `380 × 500` (최소 높이 470, DPI 자동 스케일)
- 알림음: 시작(523→659Hz 상승 두 음) / 성공(800Hz) / 실패(300Hz)로 구분

## 설정 파일

설정은 실행 파일과 같은 폴더의 `config.json`에 저장됩니다. `go run`으로 실행하는 경우(실행 파일이 임시 폴더에 생성됨)에는 `%AppData%\clip-translator\config.json`을 사용합니다.

`config.json`이 없으면 실행 시 `internal/config/default.json`에 내장된 프리셋으로 자동 생성되며, **API Key만 빈 값**으로 저장됩니다. 템플릿은 `configs/config.example.json`에서도 확인할 수 있습니다.

| 필드 | 설명 | 기본값 |
| --- | --- | --- |
| `api_key` | Gemini API Key (저장소에 커밋 금지) | `""` |
| `model` | 사용할 Gemini 모델 | `gemini-flash-lite-latest` |
| `hotkey` | 글로벌 단축키 | `Ctrl+Shift+T` |
| `always_on_top` | 항상 위 표시 | `true` |
| `active_profile` | 현재 선택된 프리셋 이름 | `WoW 북미 서버 채팅` |
| `profiles` | 이름과 시스템 프롬프트 목록 | 4개 기본 프리셋 |

단축키는 `Ctrl`, `Alt`, `Shift`, `Win` 조합과 `A-Z`, `0-9`, `F1-F20`, `Space`, `Tab`, `Enter`, `Esc`, `Delete`, 방향키를 지원합니다. 이전 기본 모델(`gemini-2.5-flash-lite`, `gemini-3.5-flash-lite`)이 설정되어 있으면 실행 시 `gemini-flash-lite-latest`로 자동 마이그레이션됩니다.

응답 지연을 줄이기 위해 `temperature`는 0.3으로 낮추고 내부 추론을 최소화합니다. Flash-Lite 계열(Gemini 3.x, `-latest`)은 `thinkingLevel: minimal`, Gemini 2.5 Flash는 `thinkingBudget: 0`을 사용하며, 그 외 모델에는 추론 옵션을 보내지 않습니다.

## 프로젝트 구조

```text
clip-translator/
├── cmd/clip-translator/main.go      # 진입점, 의존성 조립 및 GUI 실행
├── internal/config/                 # config.json 로드/저장, default.json 프리셋 템플릿
├── internal/ai/gemini.go            # Gemini REST 클라이언트 (30초 타임아웃)
├── internal/clipboard/watcher.go    # 글로벌 단축키 + 클립보드 파이프라인
├── internal/ui/window.go            # walk GUI, 상태 표시, 비프음
├── internal/ui/assets/              # 앱 아이콘/단축키 아이콘 (임베드)
├── configs/config.example.json      # 설정 템플릿
└── build/package/                   # 매니페스트, 앱 아이콘(.ico), 빌드 스크립트
```

## 보안

`config.json`에는 API Key가 평문으로 저장되며 `.gitignore`에 등록되어 있습니다. 실행 파일을 공유할 때 `config.json`을 함께 배포하지 마세요.
