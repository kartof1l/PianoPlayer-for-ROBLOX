package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-vgo/robotgo"
)

const (
	notesDir = "saved_notes" // директория для сохраненных нот
)

var (
	currentDelay   float64 = 0.1
	playing        bool    = false
	paused         bool    = false
	currentPos     int     = 0
	totalNotes     int     = 0
	stopChan       chan struct{}
	resumeChan     chan struct{}
	seekChan       chan int
	sequence       string
	parsedSequence []interface{}
	mu             sync.RWMutex
	seqMu          sync.RWMutex
	holdDuration   float64 = 0.05 // длительность удержания клавиши (50 мс по умолчанию)
	robloxOnly     bool    = true // отправлять нажатия только в Roblox
)

func init() {
	sequence = ""
	parsedSequence = []interface{}{}
	// Создаем директорию для сохраненных нот, если её нет
	os.MkdirAll(notesDir, 0755)
}

func activateRoblox() {
	if !robloxOnly {
		return
	}

	fmt.Println("🔍 Поиск окна Roblox...")

	// МЕТОД 1: Прямая активация по имени окна (самый надежный)
	titles := []string{"RobloxPlayerBeta", "Roblox Player", "Roblox"}

	for _, title := range titles {
		// Пробуем активировать по имени
		err := robotgo.ActiveName(title)
		if err == nil {
			fmt.Printf("✅ Активировано окно: %s\n", title)
			time.Sleep(100 * time.Millisecond) // Даем время на фокусировку
			return
		}
	}

	// МЕТОД 2: Поиск по PID
	pids, err := robotgo.FindIds("Roblox")
	if err == nil && len(pids) > 0 {
		for _, pid := range pids {
			err = robotgo.ActivePid(pid)
			if err == nil {
				fmt.Printf("✅ Активировано окно Roblox (PID: %d)\n", pid)
				time.Sleep(30 * time.Millisecond)
				return
			}
		}
	}

	// МЕТОД 3: Для Windows - использовать системные команды
	if runtime.GOOS == "windows" {
		// Пробуем через FindWindow
		titles := []string{"RobloxPlayerBeta", "Roblox Player", "Roblox"}
		for _, title := range titles {
			cmd := exec.Command("powershell", "-command",
				fmt.Sprintf(`(Get-Process | Where-Object {$_.MainWindowTitle -like "*%s*"}).MainWindowTitle`, title))
			output, err := cmd.Output()
			if err == nil && len(output) > 0 {
				// Нашли окно, пробуем активировать через robotgo еще раз
				robotgo.ActiveName(title)
				fmt.Printf("✅ Активировано окно (метод 3): %s\n", title)
				time.Sleep(100 * time.Millisecond)
				return
			}
		}
	}

	fmt.Println("❌ Окно Roblox не найдено! Проверьте:")
	fmt.Println("   1. Запущен ли Roblox")
	fmt.Println("   2. Не свернуто ли окно в трей")
	fmt.Println("   3. Название окна (английская версия?)")
}
func handleSaveSequence(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	sequence := r.FormValue("sequence")

	if name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	err := saveSequenceToFile(name, sequence)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(fmt.Sprintf("Сохранено как: %s", name)))
}

func handleLoadSequence(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	sequence, err := loadSequenceFromFile(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(sequence))
}

func saveSequenceToFile(name string, content string) error {
	filename := filepath.Join(notesDir, name+".txt")
	return ioutil.WriteFile(filename, []byte(content), 0644)
}

func loadSequenceFromFile(name string) (string, error) {
	filename := filepath.Join(notesDir, name+".txt")
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func listSavedSequences() ([]string, error) {
	files, err := ioutil.ReadDir(notesDir)
	if err != nil {
		return nil, err
	}

	var sequences []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".txt") {
			// Убираем .txt расширение
			name := strings.TrimSuffix(file.Name(), ".txt")
			sequences = append(sequences, name)
		}
	}
	return sequences, nil
}

func deleteSequenceFile(name string) error {
	filename := filepath.Join(notesDir, name+".txt")
	return os.Remove(filename)
}

func handleListSequences(w http.ResponseWriter, r *http.Request) {
	sequences, err := listSavedSequences()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sequences)
}

func handleDeleteSequence(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	err := deleteSequenceFile(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(fmt.Sprintf("Удалено: %s", name)))
}

func openBrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}

	if err != nil {
		fmt.Printf("Не удалось открыть браузер: %v\n", err)
		fmt.Printf("Пожалуйста, откройте вручную: %s\n", url)
	}
}

func getSequence() string {
	mu.RLock()
	defer mu.RUnlock()
	return sequence
}

func setSequence(newSeq string) {
	mu.Lock()
	defer mu.Unlock()
	sequence = newSeq
	// Парсим последовательность сразу при сохранении
	parsedSequence = parseSequenceInternal(newSeq)
	totalNotes = len(parsedSequence)
}

func getParsedSequence() []interface{} {
	seqMu.RLock()
	defer seqMu.RUnlock()
	return parsedSequence
}

func parseSequenceInternal(input string) []interface{} {
	var result []interface{}

	i := 0
	for i < len(input) {
		ch := input[i]

		if ch == '[' {
			j := strings.Index(input[i:], "]")
			if j == -1 {
				i++
				continue
			}
			chord := input[i+1 : i+j]
			chord = strings.TrimSpace(chord)
			if chord == "" {
				i += j + 1
				continue
			}

			var keys []string
			if strings.Contains(chord, " ") {
				keys = strings.Fields(chord)
			} else {
				for _, c := range chord {
					keys = append(keys, string(c))
				}
			}

			result = append(result, keys)
			i += j + 1

		} else if ch == '-' {
			// Тире - пауза
			result = append(result, "PAUSE")
			i++

		} else if ch != ' ' {
			result = append(result, string(ch))
			i++

		} else {
			i++
		}
	}

	return result
}

func pressKeyWithHold(key string, duration float64) {
	activateRoblox() // Вернул активацию!

	robotgo.KeyToggle(key, "down")
	time.Sleep(time.Duration(duration*1000) * time.Millisecond)
	robotgo.KeyToggle(key, "up")
}

// Функция для нажатия аккорда с удержанием (НЕ в горутине!)
func pressChordWithHold(keys []string, duration float64) {
	activateRoblox() // Активируем Roblox перед нажатием

	// Нажимаем все клавиши
	for _, key := range keys {
		if len(key) == 1 && key != " " {
			robotgo.KeyToggle(key, "down")
		}
	}

	// Держим все клавиши
	time.Sleep(time.Duration(duration*1000) * time.Millisecond)

	// Отжимаем все клавиши
	for _, key := range keys {
		if len(key) == 1 && key != " " {
			robotgo.KeyToggle(key, "up")
		}
	}
}

func playSequence() {
	if playing {
		return
	}

	playing = true
	paused = false
	stopChan = make(chan struct{})
	resumeChan = make(chan struct{})
	seekChan = make(chan int, 10)

	go func() {
		defer func() {
			playing = false
			paused = false
			currentPos = 0
		}()

		fmt.Println("Начинаю через 2 секунды...")
		time.Sleep(2 * time.Second)

		// Активируем Roblox перед началом
		if robloxOnly {
			activateRoblox()
			time.Sleep(500 * time.Millisecond)
		}

		fmt.Println("=== Воспроизведение (Roblox mode) ===")

		tokens := getParsedSequence()
		if len(tokens) == 0 {
			fmt.Println("Ошибка: последовательность пуста!")
			return
		}

		totalNotes = len(tokens)

		// Переменная для отслеживания времени следующей ноты
		nextNoteTime := time.Now()

		// Константа для единого множителя времени
		const timeMultiplier = 1000.0 // Все задержки в миллисекундах

		for i := currentPos; i < len(tokens); i++ {
			// Проверка запроса на перемотку
			select {
			case newPos := <-seekChan:
				if newPos >= 0 && newPos < len(tokens) {
					i = newPos
					currentPos = i
					fmt.Printf("Перемотка на позицию %d/%d\n", currentPos+1, totalNotes)
					nextNoteTime = time.Now()
				}
			default:
			}

			// Проверка остановки
			select {
			case <-stopChan:
				fmt.Println("\nВоспроизведение остановлено")
				return
			default:
			}

			// Проверка паузы
			if paused {
				fmt.Println("Пауза... (нажмите Resume для продолжения)")
				select {
				case <-resumeChan:
					paused = false
					fmt.Println("Продолжаю воспроизведение...")
					nextNoteTime = time.Now()
					if robloxOnly {
						activateRoblox()
					}
				case newPos := <-seekChan:
					if newPos >= 0 && newPos < len(tokens) {
						i = newPos
						currentPos = i
						fmt.Printf("Перемотка на позицию %d/%d\n", currentPos+1, totalNotes)
						paused = false
						nextNoteTime = time.Now()
						if robloxOnly {
							activateRoblox()
						}
					}
				case <-stopChan:
					fmt.Println("\nВоспроизведение остановлено из паузы")
					return
				}
			}

			currentPos = i

			// Ждем нужное время до начала этой ноты
			now := time.Now()
			if nextNoteTime.After(now) {
				sleepDuration := nextNoteTime.Sub(now)
				time.Sleep(sleepDuration)
			}

			// Засекаем время начала воспроизведения
			noteStartTime := time.Now()

			// Воспроизводим ноту/аккорд
			switch v := tokens[i].(type) {
			case string:
				if v == "PAUSE" {
					// Для паузы - просто ждем
					fmt.Printf("[%d/%d][%.3fs] Пауза\n",
						currentPos+1, totalNotes, currentDelay)

					// Увеличиваем время следующей ноты на длительность паузы
					nextNoteTime = noteStartTime.Add(time.Duration(currentDelay*timeMultiplier) * time.Millisecond)

				} else if len(v) == 1 && v != " " {
					// Для одиночной ноты - нажимаем и держим
					pressKeyWithHold(v, holdDuration)

					fmt.Printf("[%d/%d][%.3fs] Клавиша: %s (удержание: %.0fms)\n",
						currentPos+1, totalNotes, currentDelay, v, holdDuration*1000)

					// Увеличиваем время следующей ноты на полную задержку
					// (не вычитаем время удержания, так как оно уже включено)
					nextNoteTime = noteStartTime.Add(time.Duration(currentDelay*timeMultiplier) * time.Millisecond)
				}

			case []string:
				if len(v) > 0 {
					// Для аккорда - используем синхронную функцию (не горутину!)
					pressChordWithHold(v, holdDuration)

					fmt.Printf("[%d/%d][%.3fs] Аккорд: %v (удержание: %.0fms)\n",
						currentPos+1, totalNotes, currentDelay, v, holdDuration*1000)

					// Увеличиваем время следующей ноты на полную задержку
					nextNoteTime = noteStartTime.Add(time.Duration(currentDelay*timeMultiplier) * time.Millisecond)
				}
			}

			// Периодически реактивируем Roblox (реже, чтобы не мешать)
			if robloxOnly && i%50 == 0 && i > 0 {
				activateRoblox()
			}
		}

		fmt.Println("=== Воспроизведение завершено ===")
		currentPos = 0
	}()
}

func seekToPosition(pos int) {
	if playing && seekChan != nil {
		// Преобразуем процент в позицию
		tokens := getParsedSequence()
		if len(tokens) > 0 {
			actualPos := int(float64(pos) / 100.0 * float64(len(tokens)))
			if actualPos < 0 {
				actualPos = 0
			}
			if actualPos >= len(tokens) {
				actualPos = len(tokens) - 1
			}
			select {
			case seekChan <- actualPos:
				currentPos = actualPos
			default:
			}
		}
	}
}

func pausePlayback() {
	if playing && !paused {
		paused = true
		fmt.Println("Воспроизведение приостановлено")
	}
}

func resumePlayback() {
	if playing && paused {
		paused = false
		select {
		case resumeChan <- struct{}{}:
		default:
		}
	}
}

func stopPlayback() {
	if playing {
		playing = false
		paused = false
		currentPos = 0
		if stopChan != nil {
			close(stopChan)
		}
		fmt.Println("Воспроизведение остановлено")
	}
}

func handleSetHold(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	holdStr := r.URL.Query().Get("hold")
	if holdStr != "" {
		hold, err := strconv.ParseFloat(holdStr, 64)
		if err == nil && hold >= 0.01 && hold <= 0.2 {
			holdDuration = hold
			fmt.Printf("Удержание установлено: %.3f сек\n", holdDuration)
		}
	}

	w.Write([]byte("OK"))
}

// HTTP handlers
func handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl := `<!DOCTYPE html>
<html>
<head>
    <title>Piano Player Control - Roblox Edition</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        * { box-sizing: border-box; }
        body { 
            font-family: Arial, sans-serif; 
            margin: 0;
            padding: 20px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
        }
        .container { max-width: 900px; margin: 0 auto; }
        .control-panel {
            background: rgba(255, 255, 255, 0.95);
            padding: 25px;
            border-radius: 15px;
            box-shadow: 0 10px 30px rgba(0,0,0,0.2);
            margin-bottom: 20px;
        }
        h2 { color: #333; margin-top: 0; text-align: center; font-size: 28px; }
        
        .tabs {
            display: flex;
            margin-bottom: 20px;
            border-bottom: 2px solid #ddd;
        }
        .tab {
            padding: 10px 20px;
            cursor: pointer;
            background: #f0f0f0;
            border: none;
            border-radius: 5px 5px 0 0;
            margin-right: 5px;
            font-weight: bold;
        }
        .tab.active {
            background: #667eea;
            color: white;
        }
        .tab-content {
            display: none;
        }
        .tab-content.active {
            display: block;
        }
        
        .slider-container { margin: 15px 0; padding: 15px; background: #f8f9fa; border-radius: 10px; }
        .slider {
            width: 100%;
            height: 20px;
            -webkit-appearance: none;
            background: #ddd;
            outline: none;
            border-radius: 10px;
        }
        .slider::-webkit-slider-thumb {
            -webkit-appearance: none;
            width: 30px;
            height: 30px;
            background: #fff;
            border-radius: 50%;
            box-shadow: 0 2px 10px rgba(0,0,0,0.2);
            cursor: pointer;
            border: 3px solid #667eea;
        }
        
        .progress-slider {
            background: linear-gradient(90deg, #4CAF50 0%, #FFC107 50%, #F44336 100%);
        }
        .delay-slider {
            background: linear-gradient(90deg, #4CAF50 0%, #FFC107 50%, #F44336 100%);
        }
        .hold-slider {
            background: linear-gradient(90deg, #9C27B0 0%, #673AB7 50%, #3F51B5 100%);
        }
        
        .value-display {
            font-size: 32px;
            font-weight: bold;
            color: #333;
            margin: 15px 0;
            text-align: center;
            background: linear-gradient(90deg, #667eea, #764ba2);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }
        .hold-display {
            font-size: 32px;
            font-weight: bold;
            color: #333;
            margin: 15px 0;
            text-align: center;
            background: linear-gradient(90deg, #9C27B0, #673AB7);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }
        .progress-display {
            font-size: 18px;
            color: #666;
            text-align: center;
            margin: 10px 0;
        }
        .button {
            background: linear-gradient(90deg, #667eea 0%, #764ba2 100%);
            color: white;
            border: none;
            padding: 12px 20px;
            margin: 5px;
            border-radius: 25px;
            cursor: pointer;
            font-size: 14px;
            font-weight: bold;
            transition: all 0.3s;
            box-shadow: 0 4px 15px rgba(102, 126, 234, 0.3);
        }
        .button:hover {
            transform: translateY(-2px);
            box-shadow: 0 6px 20px rgba(102, 126, 234, 0.4);
        }
        .button.stop { background: linear-gradient(90deg, #F44336 0%, #E91E63 100%); }
        .button.pause { background: linear-gradient(90deg, #FF9800 0%, #FF5722 100%); }
        .button.resume { background: linear-gradient(90deg, #4CAF50 0%, #8BC34A 100%); }
        .button-group {
            display: flex;
            flex-wrap: wrap;
            justify-content: center;
            gap: 8px;
            margin: 15px 0;
        }
        .status {
            margin: 20px 0;
            padding: 15px;
            background: #e8f5e9;
            border-radius: 10px;
            text-align: center;
            font-weight: bold;
            font-size: 16px;
            border-left: 5px solid #4CAF50;
        }
        .sequence-input {
            width: 100%;
            height: 200px;
            margin: 15px 0;
            padding: 15px;
            border: 2px solid #ddd;
            border-radius: 10px;
            font-family: 'Courier New', monospace;
            font-size: 16px;
            resize: vertical;
        }
        .saved-list {
            max-height: 400px;
            overflow-y: auto;
            border: 1px solid #ddd;
            border-radius: 10px;
            padding: 10px;
            margin: 15px 0;
        }
        .saved-item {
            padding: 15px;
            margin: 5px 0;
            background: #f8f9fa;
            border-radius: 8px;
            cursor: pointer;
            display: flex;
            justify-content: space-between;
            align-items: center;
            transition: all 0.3s;
        }
        .saved-item:hover {
            background: #e9ecef;
            transform: translateX(5px);
        }
        .saved-item-name {
            font-weight: bold;
            color: #333;
        }
        .saved-item-actions {
            display: flex;
            gap: 5px;
        }
        .saved-item-btn {
            padding: 5px 10px;
            border: none;
            border-radius: 5px;
            cursor: pointer;
            font-size: 12px;
        }
        .btn-load {
            background: #4CAF50;
            color: white;
        }
        .btn-delete {
            background: #F44336;
            color: white;
        }
        .save-input {
            width: 100%;
            padding: 10px;
            margin: 10px 0;
            border: 2px solid #ddd;
            border-radius: 5px;
            font-size: 16px;
        }
        .roblox-badge {
            background: #ff0000;
            color: white;
            padding: 5px 10px;
            border-radius: 20px;
            font-size: 14px;
            display: inline-block;
            margin-bottom: 10px;
        }
        .format-help {
            background: #e3f2fd;
            padding: 10px;
            border-radius: 5px;
            margin: 10px 0;
            font-size: 14px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="control-panel">
            <h2>🎹 Piano Player - Roblox Mode</h2>
            <div class="roblox-badge">🎮 Нажатия только в Roblox</div>
            
            <div class="tabs">
                <button class="tab active" onclick="switchTab('editor')">Редактор</button>
                <button class="tab" onclick="switchTab('saved')">Сохраненные</button>
            </div>
            
            <!-- Вкладка редактора -->
            <div id="editor-tab" class="tab-content active">
                <div class="value-display" id="delayValue">Задержка: 0.100 сек</div>
                
                <div class="slider-container">
                    <input type="range" min="10" max="500" value="100" class="slider delay-slider" id="delaySlider">
                </div>
                
                <div class="button-group">
                    <button class="button" onclick="changeDelay(-10)">-0.01s</button>
                    <button class="button" onclick="changeDelay(10)">+0.01s</button>
                    <button class="button" onclick="resetDelay()">Сброс (0.1s)</button>
                </div>
                
                <div class="hold-display" id="holdValue">Удержание: 0.050 сек</div>
                
                <div class="slider-container">
                    <input type="range" min="10" max="200" value="50" class="slider hold-slider" id="holdSlider">
                </div>
                
                <div class="button-group">
                    <button class="button" onclick="changeHold(-10)">-0.01s</button>
                    <button class="button" onclick="changeHold(10)">+0.01s</button>
                    <button class="button" onclick="resetHold()">Сброс (0.05s)</button>
                </div>
                
                <div class="progress-display" id="progressValue">Прогресс: 0/0 (0%)</div>
                
                <div class="slider-container">
                    <input type="range" min="0" max="100" value="0" class="slider progress-slider" id="progressSlider">
                </div>
                
                <div class="button-group">
                    <button class="button" onclick="play()">▶ Воспроизвести</button>
                    <button class="button pause" onclick="pause()">⏸ Пауза</button>
                    <button class="button resume" onclick="resume()">▶ Продолжить</button>
                    <button class="button stop" onclick="stop()">⏹ Стоп</button>
                </div>
                
                <div class="status" id="status">
                    Готово
                </div>
                
                <div class="format-help">
                    <strong>Формат нот (Roblox):</strong><br>
                    • <code>q w e r t y u i o p</code> - ноты<br>
                    • <code>[q w e]</code> - аккорд (нажимаются вместе)<br>
                    • <code>-</code> - пауза<br>
                    • Пример: <code>q w e - [q w e] r t y</code>
                </div>
                
                <h3>🎵 Введите последовательность нот:</h3>
                <textarea class="sequence-input" id="sequence" placeholder="Введите ноты здесь...">{{.Sequence}}</textarea>
                
                <div class="button-group">
                    <button class="button" onclick="updateSequence()">💾 Сохранить в текущий</button>
                    <button class="button" onclick="showSaveDialog()">💾 Сохранить как...</button>
                    <button class="button" onclick="clearSequence()">🗑 Очистить</button>
                </div>
            </div>
            
            <!-- Вкладка сохраненных файлов -->
            <div id="saved-tab" class="tab-content">
                <h3>📁 Сохраненные последовательности</h3>
                
                <div class="saved-list" id="savedList">
                    Загрузка...
                </div>
                
                <div style="margin-top: 20px;">
                    <input type="text" class="save-input" id="newSaveName" placeholder="Имя нового файла">
                    <button class="button" onclick="saveCurrentToNewFile()">💾 Сохранить текущую как новый файл</button>
                </div>
            </div>
        </div>
    </div>
    
    <script>
        let currentDelay = 100;
        let currentHold = 50;
        let currentProgress = 0;
        let totalNotes = 0;
        
        function switchTab(tab) {
            document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
            
            if (tab === 'editor') {
                document.querySelectorAll('.tab')[0].classList.add('active');
                document.getElementById('editor-tab').classList.add('active');
                loadCurrentSequence();
            } else {
                document.querySelectorAll('.tab')[1].classList.add('active');
                document.getElementById('saved-tab').classList.add('active');
                loadSavedList();
            }
        }
        
        function loadSavedList() {
            fetch('/list_sequences')
                .then(response => response.json())
                .then(data => {
                    const list = document.getElementById('savedList');
                    if (data.length === 0) {
                        list.innerHTML = '<p style="text-align: center; color: #999;">Нет сохраненных файлов</p>';
                        return;
                    }
                    
                    let html = '';
                    data.forEach(name => {
                        html += '<div class="saved-item">' +
                            '<span class="saved-item-name">' + name + '</span>' +
                            '<div class="saved-item-actions">' +
                            '<button class="saved-item-btn btn-load" onclick="loadSaved(\'' + name + '\')">Загрузить</button>' +
                            '<button class="saved-item-btn btn-delete" onclick="deleteSaved(\'' + name + '\')">Удалить</button>' +
                            '</div>' +
                            '</div>';
                    });
                    list.innerHTML = html;
                });
        }

        
        function loadSaved(name) {
            fetch('/load_sequence?name=' + encodeURIComponent(name))
                .then(response => response.text())
                .then(text => {
                    document.getElementById('sequence').value = text;
                    switchTab('editor');
                    showNotification('Загружено: ' + name);
                    updateSequence(); // Автоматически сохраняем в текущий буфер
                });
        }
        
        function deleteSaved(name) {
            if (confirm('Удалить файл "' + name + '"?')) {
                fetch('/delete_sequence?name=' + encodeURIComponent(name), { method: 'POST' })
                    .then(response => response.text())
                    .then(text => {
                        showNotification(text);
                        loadSavedList();
                    });
            }
        }
        
        function showSaveDialog() {
            const name = prompt('Введите имя для сохранения:');
            if (name) {
                saveSequence(name);
            }
        }
        
        function saveSequence(name) {
            const seq = document.getElementById('sequence').value;
            fetch('/save_sequence', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: 'name=' + encodeURIComponent(name) + '&sequence=' + encodeURIComponent(seq)
            })
            .then(response => response.text())
            .then(text => {
                showNotification(text);
            });
        }
        
        function saveCurrentToNewFile() {
            const name = document.getElementById('newSaveName').value.trim();
            if (name) {
                saveSequence(name);
                document.getElementById('newSaveName').value = '';
            } else {
                alert('Введите имя файла');
            }
        }
        
        function loadCurrentSequence() {
            fetch('/get_sequence')
                .then(response => response.text())
                .then(text => {
                    document.getElementById('sequence').value = text;
                });
        }
        
        function updateDisplay() {
            const delaySec = (currentDelay / 1000).toFixed(3);
            const holdSec = (currentHold / 1000).toFixed(3);
            document.getElementById('delayValue').textContent = 'Задержка: ' + delaySec + ' сек';
            document.getElementById('holdValue').textContent = 'Удержание: ' + holdSec + ' сек';
            document.getElementById('delaySlider').value = currentDelay;
            document.getElementById('holdSlider').value = currentHold;
            
            const percent = totalNotes > 0 ? Math.round((currentProgress / totalNotes) * 100) : 0;
            document.getElementById('progressValue').textContent = 
                'Прогресс: ' + currentProgress + '/' + totalNotes + ' (' + percent + '%)';
            document.getElementById('progressSlider').value = percent;
        }
        
        function sendDelay() {
            fetch('/set_delay?delay=' + (currentDelay / 1000), { method: 'POST' });
        }
        
        function sendHold() {
            fetch('/set_hold?hold=' + (currentHold / 1000), { method: 'POST' });
        }
        
        function changeDelay(delta) {
            currentDelay = Math.max(10, Math.min(500, currentDelay + delta));
            updateDisplay();
            sendDelay();
        }
        
        function changeHold(delta) {
            currentHold = Math.max(10, Math.min(200, currentHold + delta));
            updateDisplay();
            sendHold();
        }
        
        function resetDelay() {
            currentDelay = 100;
            updateDisplay();
            sendDelay();
        }
        
        function resetHold() {
            currentHold = 50;
            updateDisplay();
            sendHold();
        }
        
        function play() {
            fetch('/play', { method: 'POST' })
                .then(response => response.text())
                .then(text => {
                    document.getElementById('status').textContent = text;
                });
        }
        
        function pause() {
            fetch('/pause', { method: 'POST' })
                .then(response => response.text())
                .then(text => {
                    document.getElementById('status').textContent = text;
                });
        }
        
        function resume() {
            fetch('/resume', { method: 'POST' })
                .then(response => response.text())
                .then(text => {
                    document.getElementById('status').textContent = text;
                });
        }
        
        function stop() {
            fetch('/stop', { method: 'POST' })
                .then(response => response.text())
                .then(text => {
                    document.getElementById('status').textContent = text;
                    currentProgress = 0;
                    updateDisplay();
                });
        }
        
        function seekToPercent(percent) {
            fetch('/seek?percent=' + percent, { method: 'POST' })
                .then(response => response.text())
                .then(text => {
                    document.getElementById('status').textContent = text;
                });
        }
        
        function updateSequence() {
            const seq = document.getElementById('sequence').value;
            fetch('/set_sequence', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: 'sequence=' + encodeURIComponent(seq)
            })
            .then(response => response.text())
            .then(text => {
                document.getElementById('status').textContent = text;
                showNotification('Последовательность сохранена!');
                fetch('/get_sequence_info')
                    .then(response => response.json())
                    .then(data => {
                        totalNotes = data.total_notes || 0;
                        currentProgress = 0;
                        updateDisplay();
                    });
            });
        }
        
        function clearSequence() {
            document.getElementById('sequence').value = '';
            showNotification('Поле очищено!');
        }
        
        function showNotification(message) {
            const statusEl = document.getElementById('status');
            statusEl.textContent = message;
            statusEl.style.background = '#d4edda';
            statusEl.style.borderLeftColor = '#28a745';
            
            setTimeout(function() {
                statusEl.style.background = '#e8f5e9';
                statusEl.style.borderLeftColor = '#4CAF50';
            }, 3000);
        }
        
        // Инициализация слайдеров
        document.getElementById('delaySlider').addEventListener('input', function(e) {
            currentDelay = parseInt(e.target.value);
            updateDisplay();
            sendDelay();
        });
        
        document.getElementById('holdSlider').addEventListener('input', function(e) {
            currentHold = parseInt(e.target.value);
            updateDisplay();
            sendHold();
        });
        
        document.getElementById('progressSlider').addEventListener('input', function(e) {
            const percent = parseInt(e.target.value);
            document.getElementById('progressValue').textContent = 
                'Прогресс: ' + currentProgress + '/' + totalNotes + ' (' + percent + '%)';
        });
        
        document.getElementById('progressSlider').addEventListener('change', function(e) {
            seekToPercent(parseInt(e.target.value));
        });
        
        // Обновление статуса
        function updateStatus() {
            fetch('/get_status')
                .then(response => response.json())
                .then(data => {
                    if (data.playing) {
                        if (data.paused) {
                            document.getElementById('status').textContent = '⏸ Пауза на позиции ' + data.position + '/' + data.total_notes;
                        } else {
                            document.getElementById('status').textContent = '▶ Воспроизведение... ' + data.position + '/' + data.total_notes;
                        }
                    } else {
                        document.getElementById('status').textContent = 'Готово';
                    }
                    
                    currentProgress = data.position || 0;
                    totalNotes = data.total_notes || 0;
                    
                    // Синхронизируем задержку и удержание с сервером
                    if (data.delay && data.delay !== currentDelay/1000) {
                        currentDelay = Math.round(data.delay * 1000);
                    }
                    if (data.hold && data.hold !== currentHold/1000) {
                        currentHold = Math.round(data.hold * 1000);
                    }
                    
                    updateDisplay();
                });
        }
        
        window.addEventListener('load', function() {
            updateDisplay();
            setInterval(updateStatus, 1000);
        });
    </script>
</body>
</html>`

	// Получаем IP адреса
	ipAddrs := getLocalIPs()

	// Определяем порт
	port := "5555"
	if r.URL.Port() != "" {
		port = r.URL.Port()
	}

	// Подготовка данных для шаблона
	data := struct {
		Sequence string
		IPs      []string
		Port     string
	}{
		Sequence: getSequence(),
		IPs:      ipAddrs,
		Port:     port,
	}

	t, err := template.New("index").Parse(tmpl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = t.Execute(w, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleSetDelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	delayStr := r.URL.Query().Get("delay")
	if delayStr != "" {
		delay, err := strconv.ParseFloat(delayStr, 64)
		if err == nil && delay >= 0.01 && delay <= 0.5 {
			currentDelay = delay
			fmt.Printf("Задержка установлена: %.3f сек\n", currentDelay)
		}
	}

	w.Write([]byte("OK"))
}

func handlePlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	go playSequence()
	w.Write([]byte("Воспроизведение начато"))
}

func handlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pausePlayback()
	w.Write([]byte("Воспроизведение приостановлено"))
}

func handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resumePlayback()
	w.Write([]byte("Воспроизведение продолжено"))
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stopPlayback()
	w.Write([]byte("Воспроизведение остановлено"))
}

func handleSeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	percentStr := r.URL.Query().Get("percent")
	if percentStr != "" {
		percent, err := strconv.Atoi(percentStr)
		if err == nil && percent >= 0 && percent <= 100 {
			seekToPosition(percent)
			fmt.Printf("Перемотка на %d%%\n", percent)
			w.Write([]byte(fmt.Sprintf("Перемотка на %d%%", percent)))
			return
		}
	}

	http.Error(w, "Invalid percentage", http.StatusBadRequest)
}

func handleSetSequence(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sequence := r.FormValue("sequence")
	setSequence(sequence)
	fmt.Printf("Последовательность установлена (%d нот)\n", totalNotes)
	w.Write([]byte("Последовательность сохранена"))
}

func handleGetSequence(w http.ResponseWriter, r *http.Request) {
	sequence := getSequence()
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(sequence))
}

func handleGetStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"playing":     playing,
		"paused":      paused,
		"position":    currentPos,
		"total_notes": totalNotes,
		"delay":       currentDelay,
		"hold":        holdDuration,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func handleGetSequenceInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"total_notes": totalNotes,
		"has_notes":   totalNotes > 0,
		"delay":       currentDelay,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func getLocalIPs() []string {
	var ips []string

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}

	return ips
}

func main() {
	// Устанавливаем начальную последовательность
	setSequence("q w e r t y u i o p - [q w e] [r t y] [u i o]")

	// Настраиваем HTTP маршруты
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/set_delay", handleSetDelay)
	http.HandleFunc("/set_hold", handleSetHold)
	http.HandleFunc("/play", handlePlay)
	http.HandleFunc("/pause", handlePause)
	http.HandleFunc("/resume", handleResume)
	http.HandleFunc("/stop", handleStop)
	http.HandleFunc("/seek", handleSeek)
	http.HandleFunc("/set_sequence", handleSetSequence)
	http.HandleFunc("/get_sequence", handleGetSequence)
	http.HandleFunc("/get_status", handleGetStatus)
	http.HandleFunc("/get_sequence_info", handleGetSequenceInfo)

	// Новые маршруты для работы с файлами
	http.HandleFunc("/save_sequence", handleSaveSequence)
	http.HandleFunc("/load_sequence", handleLoadSequence)
	http.HandleFunc("/list_sequences", handleListSequences)
	http.HandleFunc("/delete_sequence", handleDeleteSequence)

	port := ":5555"
	url := fmt.Sprintf("http://localhost%s", port)

	fmt.Println("🎹 Piano Player - Roblox Mode")
	fmt.Println("=" + strings.Repeat("=", 40))
	fmt.Printf("Сервер запущен на порту %s\n", port)
	fmt.Println("Откройте в браузере:")
	fmt.Println(url)
	fmt.Println("\n📁 Ноты сохраняются в папку: " + notesDir)
	fmt.Println("🎮 Нажатия отправляются только в Roblox")
	fmt.Printf("⚙️ Задержка: %.2fс, Удержание: %.2fс\n", currentDelay, holdDuration)

	// Открываем браузер
	go func() {
		time.Sleep(500 * time.Millisecond)
		openBrowser(url)
	}()

	err := http.ListenAndServe(port, nil)
	if err != nil {
		fmt.Printf("Ошибка запуска сервера: %v\n", err)
	}
}
