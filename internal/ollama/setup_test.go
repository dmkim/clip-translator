package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestMaxPercentInChunk(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "single", data: "45%", want: 45},
		{name: "multiple", data: "10%\r55%", want: 55},
		{name: "max across chunk", data: "99% and 5%", want: 99},
		{name: "complete", data: "100%", want: 100},
		{name: "none", data: "pulling manifest", want: -1},
		{name: "out of range", data: "101%", want: -1},
		{name: "no digits", data: "abc%", want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxPercentInChunk([]byte(tt.data)); got != tt.want {
				t.Errorf("maxPercentInChunk(%q) = %d, want %d", tt.data, got, tt.want)
			}
		})
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		detected bool
		gpu      string
		vram     int
	}{
		{
			name:     "single gpu",
			output:   "NVIDIA GeForce MX250, 2048\n",
			detected: true,
			gpu:      "NVIDIA GeForce MX250",
			vram:     2048,
		},
		{
			name:     "choose largest",
			output:   "Intel(R) UHD Graphics, 128\nNVIDIA GeForce RTX 3060, 12288\n",
			detected: true,
			gpu:      "NVIDIA GeForce RTX 3060",
			vram:     12288,
		},
		{
			name:     "units suffix",
			output:   "NVIDIA GeForce GTX 1650, 4096 MiB\n",
			detected: true,
			gpu:      "NVIDIA GeForce GTX 1650",
			vram:     4096,
		},
		{
			name:     "bom prefix",
			output:   "\ufeffNVIDIA GeForce GTX 1650, 4096\n",
			detected: true,
			gpu:      "NVIDIA GeForce GTX 1650",
			vram:     4096,
		},
		{
			name:   "empty",
			output: "",
		},
		{
			name:   "invalid",
			output: "not a gpu line\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNvidiaSMI(tt.output)
			if got.Detected != tt.detected {
				t.Fatalf("Detected = %v, want %v", got.Detected, tt.detected)
			}
			if got.GPU != tt.gpu {
				t.Errorf("GPU = %q, want %q", got.GPU, tt.gpu)
			}
			if got.VRAMMB != tt.vram {
				t.Errorf("VRAMMB = %d, want %d", got.VRAMMB, tt.vram)
			}
		})
	}
}

func TestSelectModel(t *testing.T) {
	tests := []struct {
		name     string
		hardware Hardware
		want     string
	}{
		{name: "not detected", hardware: Hardware{}, want: Model1B},
		{name: "low vram", hardware: Hardware{Detected: true, VRAMMB: 2048}, want: Model1B},
		{name: "threshold", hardware: Hardware{Detected: true, VRAMMB: 6144}, want: Model4B},
		{name: "high vram", hardware: Hardware{Detected: true, VRAMMB: 12288}, want: Model4B},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectModel(tt.hardware); got != tt.want {
				t.Errorf("selectModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMemoryMB(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "plain", value: "2048", want: 2048},
		{name: "units", value: "4096 MiB", want: 4096},
		{name: "empty", value: "", wantErr: true},
		{name: "invalid", value: "abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMemoryMB(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMemoryMB() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseMemoryMB() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWarmupModel(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
		}))
		defer srv.Close()

		if err := warmupModel(context.Background(), srv.URL+"/v1", "gemma3:1b"); err != nil {
			t.Fatalf("warmupModel() error = %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"model crashed"}}`))
		}))
		defer srv.Close()

		if err := warmupModel(context.Background(), srv.URL+"/v1", "gemma3:1b"); err == nil {
			t.Fatal("expected a warmup error")
		}
	})
}

func TestNativeEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		want    string
		wantErr bool
	}{
		{name: "v1", base: "http://localhost:11434/v1", want: "http://localhost:11434/api/ps"},
		{name: "trailing slash", base: "http://localhost:11434/v1/", want: "http://localhost:11434/api/ps"},
		{name: "no v1", base: "http://localhost:11434", want: "http://localhost:11434/api/ps"},
		{name: "missing scheme", base: "localhost:11434/v1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nativeEndpoint(tt.base, "/api/ps")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("nativeEndpoint() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("nativeEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPinModel(t *testing.T) {
	t.Cleanup(func() {
		pinnedMu.Lock()
		pinnedBaseURL = ""
		pinnedModel = ""
		pinnedMu.Unlock()
	})

	var keepAlive any
	var model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path = %s, want /api/generate", r.URL.Path)
		}
		var body struct {
			Model     string `json:"model"`
			KeepAlive any    `json:"keep_alive"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		model = body.Model
		keepAlive = body.KeepAlive
		_, _ = w.Write([]byte(`{"done":true}`))
	}))
	defer srv.Close()

	if err := PinModel(context.Background(), srv.URL+"/v1", "gemma3:1b"); err != nil {
		t.Fatalf("PinModel() error = %v", err)
	}
	if model != "gemma3:1b" {
		t.Errorf("model = %q, want gemma3:1b", model)
	}
	if keepAlive != float64(-1) {
		t.Errorf("keep_alive = %v, want -1", keepAlive)
	}

	pinnedMu.Lock()
	defer pinnedMu.Unlock()
	if pinnedModel != "gemma3:1b" || pinnedBaseURL != srv.URL+"/v1" {
		t.Errorf("pinned state = (%q, %q)", pinnedBaseURL, pinnedModel)
	}
}

func TestUnloadModel(t *testing.T) {
	t.Run("loaded", func(t *testing.T) {
		var unloadCalled bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/ps":
				_, _ = w.Write([]byte(`{"models":[{"name":"gemma3:1b"}]}`))
			case "/api/generate":
				unloadCalled = true
				var body struct {
					KeepAlive any `json:"keep_alive"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if body.KeepAlive != float64(0) {
					t.Errorf("keep_alive = %v, want 0", body.KeepAlive)
				}
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		}))
		defer srv.Close()

		if err := UnloadModel(context.Background(), srv.URL+"/v1", "gemma3:1b"); err != nil {
			t.Fatalf("UnloadModel() error = %v", err)
		}
		if !unloadCalled {
			t.Error("expected unload request")
		}
	})

	t.Run("not loaded", func(t *testing.T) {
		var generateCalled bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/ps":
				_, _ = w.Write([]byte(`{"models":[]}`))
			case "/api/generate":
				generateCalled = true
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		}))
		defer srv.Close()

		if err := UnloadModel(context.Background(), srv.URL+"/v1", "gemma3:1b"); err != nil {
			t.Fatalf("UnloadModel() error = %v", err)
		}
		if generateCalled {
			t.Error("did not expect an unload request")
		}
	})
}

func TestWithEnv(t *testing.T) {
	env := []string{"PATH=C:\\Windows", "OLLAMA_HOST=old", "TEMP=C:\\Temp"}
	got := withEnv(env, "OLLAMA_HOST", "127.0.0.1:11434")
	want := []string{"PATH=C:\\Windows", "TEMP=C:\\Temp", "OLLAMA_HOST=127.0.0.1:11434"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withEnv() = %v, want %v", got, want)
	}
}
