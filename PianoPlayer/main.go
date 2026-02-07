package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-vgo/robotgo"
)

var (
	currentDelay   float64 = 0.1
	playing        bool    = false
	paused         bool    = false
	currentPos     int     = 0
	totalNotes     int     = 0
	stopChan       chan struct{}
	pauseChan      chan struct{}
	resumeChan     chan struct{}
	seekChan       chan int
	sequence       string
	parsedSequence []interface{}
	mu             sync.RWMutex
	seqMu          sync.RWMutex
)

func init() {
	sequence = ""
	parsedSequence = []interface{}{}
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

func setParsedSequence(seq []interface{}) {
	seqMu.Lock()
	defer seqMu.Unlock()
	parsedSequence = seq
	totalNotes = len(seq)
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
			// Тире - пауза (задержка в 2 раза больше обычной)
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

func playSequence() {
	if playing {
		return
	}

	playing = true
	paused = false
	stopChan = make(chan struct{})
	pauseChan = make(chan struct{})
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
		fmt.Println("=== Воспроизведение ===")

		// Получаем уже распарсенную последовательность
		tokens := getParsedSequence()
		if len(tokens) == 0 {
			fmt.Println("Ошибка: последовательность пуста!")
			return
		}

		totalNotes = len(tokens)

		// Воспроизводим с текущей позиции
		for i := currentPos; i < len(tokens); i++ {
			// Проверка запроса на перемотку
			select {
			case newPos := <-seekChan:
				if newPos >= 0 && newPos < len(tokens) {
					i = newPos
					currentPos = i
					fmt.Printf("Перемотка на позицию %d/%d\n", currentPos+1, totalNotes)
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
				case newPos := <-seekChan:
					if newPos >= 0 && newPos < len(tokens) {
						i = newPos
						currentPos = i
						fmt.Printf("Перемотка на позицию %d/%d\n", currentPos+1, totalNotes)
						paused = false
					}
				case <-stopChan:
					fmt.Println("\nВоспроизведение остановлено из паузы")
					return
				}
			}

			// Обновляем текущую позицию
			currentPos = i

			switch v := tokens[i].(type) {
			case string:
				if v == "PAUSE" {
					// Удвоенная пауза для тире
					time.Sleep(time.Duration(currentDelay*2000) * time.Millisecond)
					fmt.Printf("[%d/%d][%.3fs] Пауза\n", currentPos+1, totalNotes, currentDelay*2)
				} else if len(v) == 1 && v != " " {
					robotgo.KeyTap(v)
					fmt.Printf("[%d/%d][%.3fs] Клавиша: %s\n", currentPos+1, totalNotes, currentDelay, v)
				}

			case []string:
				if len(v) > 0 {
					// Нажимаем все клавиши аккорда
					for _, key := range v {
						if len(key) == 1 && key != " " {
							robotgo.KeyToggle(key, "down")
						}
					}
					// Очень короткая задержка для одновременности
					time.Sleep(20 * time.Millisecond)
					// Отжимаем все клавиши
					for _, key := range v {
						if len(key) == 1 && key != " " {
							robotgo.KeyToggle(key, "up")
						}
					}
					fmt.Printf("[%d/%d][%.3fs] Аккорд: %v\n", currentPos+1, totalNotes, currentDelay, v)
				}
			}

			// Обычная задержка между нотами
			time.Sleep(time.Duration(currentDelay*1000) * time.Millisecond)
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

// HTTP handlers
func handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl := `<!DOCTYPE html>
<html>
<head>
	<title>Piano Player Control</title>
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
		.container { max-width: 800px; margin: 0 auto; }
		.control-panel {
			background: rgba(255, 255, 255, 0.95);
			padding: 25px;
			border-radius: 15px;
			box-shadow: 0 10px 30px rgba(0,0,0,0.2);
			margin-bottom: 20px;
		}
		h2 { color: #333; margin-top: 0; text-align: center; font-size: 28px; }
		
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
		.progress-slider::-webkit-slider-thumb {
			border: 3px solid #333;
		}
		
		.delay-slider {
			background: linear-gradient(90deg, #4CAF50 0%, #FFC107 50%, #F44336 100%);
		}
		.delay-slider::-webkit-slider-thumb {
			border: 3px solid #667eea;
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
		.button.seek { background: linear-gradient(90deg, #9C27B0 0%, #673AB7 100%); }
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
		.format-help {
			background: #e3f2fd;
			padding: 10px;
			border-radius: 5px;
			margin: 10px 0;
			font-size: 14px;
		}
		.server-info {
			background: #fff3cd;
			border: 1px solid #ffeaa7;
			padding: 15px;
			border-radius: 10px;
			margin-top: 20px;
			font-size: 14px;
		}
		.time-controls {
			display: flex;
			justify-content: space-between;
			align-items: center;
			margin: 10px 0;
		}
		.time-btn {
			background: #6c757d;
			color: white;
			border: none;
			padding: 8px 15px;
			border-radius: 20px;
			cursor: pointer;
			font-size: 12px;
		}
	</style>
</head>
<body>
	<div class="container">
		<div class="control-panel">
			<h2>🎹 Piano Player Control</h2>
			<div class="server-info">
				<h3>📡 Подключение к серверу</h3>
				<p>IP адрес для доступа с других устройств: <strong id="ipAddress">Загрузка...</strong></p>
				<p>Откройте этот адрес на любом устройстве в вашей WiFi сети</p>
			</div>
			
			<div class="value-display" id="delayValue">Задержка: 0.100 сек</div>
			
			<div class="slider-container">
				<input type="range" min="10" max="500" value="100" class="slider delay-slider" id="delaySlider">
			</div>
			
			<div class="button-group">
				<button class="button" onclick="changeDelay(-10)">-0.01s</button>
				<button class="button" onclick="changeDelay(10)">+0.01s</button>
				<button class="button" onclick="resetDelay()">Сброс (0.1s)</button>
			</div>
			
			<div class="progress-display" id="progressValue">Прогресс: 0/0 (0%)</div>
			
			<div class="slider-container">
				<input type="range" min="0" max="100" value="0" class="slider progress-slider" id="progressSlider">
			</div>
			
			<div class="time-controls">
				<button class="time-btn" onclick="seekRelative(-10)">-10%</button>
				<button class="time-btn" onclick="seekRelative(-5)">-5%</button>
				<button class="time-btn" onclick="seekToStart()">⏮ Начало</button>
				<button class="time-btn" onclick="seekRelative(5)">+5%</button>
				<button class="time-btn" onclick="seekRelative(10)">+10%</button>
			</div>
			
			<div class="button-group">
				<button class="button" onclick="play()">▶ Воспроизвести</button>
				<button class="button pause" onclick="pause()">⏸ Пауза</button>
				<button class="button resume" onclick="resume()">▶ Продолжить</button>
				<button class="button stop" onclick="stop()">⏹ Стоп</button>
				<button class="button seek" onclick="seekToCurrent()">↻ Перемотать</button>
			</div>
			
			<div class="status" id="status">
				Готово
			</div>
			
			<div class="format-help">
				<strong>Формат нот:</strong><br>
				• <code>x</code> - одна нота (например: <code>1 2 3</code>)<br>
				• <code>[x y z]</code> - аккорд (например: <code>[q w e]</code>)<br>
				• <code>-</code> - пауза (удвоенная задержка)<br>
				• Пример: <code>1 2 3 - [q w e] - 4 5 6</code>
			</div>
			
			<div>
				<h3>🎵 Введите последовательность нот:</h3>
				<textarea class="sequence-input" id="sequence" placeholder="Введите ноты здесь...">{{.Sequence}}</textarea>
				<div class="button-group">
					<button class="button" onclick="updateSequence()">💾 Сохранить</button>
					<button class="button" onclick="loadSequence()">📥 Загрузить</button>
					<button class="button" onclick="clearSequence()">🗑 Очистить</button>
				</div>
			</div>
			
			<div style="margin-top: 20px; text-align: center;">
				<button class="button" onclick="loadExample('rush_e')">🎮 Rush E</button>
				<button class="button" onclick="loadExample('fnaf')">👻 FNAF</button>
				<button class="button" onclick="loadExample('enter_ninja')">🥷 Enter The Ninja</button>
				<button class="button" onclick="loadExample('simple')">🎵 Простая мелодия</button>
			</div>
		</div>
	</div>
	
	<script>
		let currentDelay = 100;
		let currentProgress = 0;
		let totalNotes = 0;
		let examples = {
			'rush_e': 'uu u u u u u u u u [uf] [uf] [uf] [uf] [uf] [ufx] [0ufx] [0ufx] [30ufx] [30ufx] [30ufx] [30ufx] [30ufx] [30ufx] [30ufx] 6 [80] 3 [80] 3 [6] [80] 3 [80] [6u] u [80u] u [3u] u [80u] u [6u] u [80u] u [3u] u [80u] u [6u] u [80u] u [3u] i [80u] Y [6u] [80p] [3s] [80] [d] d [90d] d [3d] s [90a] d [6s] s [80s] s [3s] a [80p] s [7a] a [ea] a [I] [ea] [0WO] [3] [6u] u [80u] u [3u] u [80u] u [6u] u [80u] u [3u] u [80u] u [6u] i [80u] Y [3u] [80p] s [6f] [80j] [3l] [80] [9z] l [qek] z [8l] k [0ej] l [7k] j [9qH] k [6j] f [80s] p [30g] f [Qd] s [Wa] p [30O] a [6ep] [3] [6u] u [80u] u [3u] u [80u] u [6u] u [80u] u [3u] u [80u] u [6u] u [80u] u [3u] i [80u] Y [6u] [80p] [3s] [80] [7d] d [90d] d [3d] s [90a] d [6s] s [80s] s [3s] a [80p] s [7a] a [Qa] a [I] [Qa] [0WO] [3] [6u] u [80u] u [3u] u [80u] u [6u] u [80u] u [3u] u [80u] u [6u] i [80u] Y [3u] [80p] s [6f] [80j] [3l] [80] [9z] l [qek] z [8l] k [0ej] l [7k] j [9qH] k [6j] f [80s] p [30g] f [Qd] s [Wa] p [30O] a [6ep] [6] 7 [29d] [qe] S [6d] [qef] [9g] [qef] [6d] [qeg] [8f] [0e] d [6s] [0ed] [8f] [0e] 6 [0es] [0a] [Wr] P [7a] [Wrs] [0d] [Wrs] [7a] [Wrd] [6s] [6p] [8s] [0f] [ej] [0f] [8s] [6p] [29d] [qe] S [6d] [qef] [9g] [qef] [6d] [qeg] [8f] [0e] d [6s] [0ed] [8f] [0e] [6j] [0e] 9 [qed] 7 [qeg] [30f] [18s] [29d] [7a] [6p] [O3uf] [6psj] [3] [6u] u [80u] u [3uf] [ufx] [80ufx] [ufx] [6ufx] [ufx] [80ufx] [ufx] [3ufx] [ufx] [80ufx] [ufx] [6ufx] [ufx] [80ufx] [ufx] [3ufx] i [80u] Y [6u] [80p] [3s] [80] [7d] d [90d] d [3d] s [90a] d [6s] s [80s] s [3s] a [80p] s [7a] a [Qa] a [I] [Qa] [0WO] [3] [6u] u [80u] u [3uf] [uf] [80uf] [ufx] [6ufx] [ufx] [80ufx] [ufx] [3ufx] [ufx] [80ufx] [ufx] [6ufx] i [80u] Y [3u] [80p] s [6f] [80j] [3l] [80] [9z] l [qek] z [8l] k [0ej] l [7k] j [9WH] k [6j] f [8es] p [7g] f d s [30a] p O a [6ep] [6] [80] 3 [80] 3 [6] [80] 3 [80]',
			'fnaf': '[h j] h [f f] f [d f] [g f] [g d] [h f] [s p] [d O] [6 s] [a p] [o s] [u o] p [o i] [u y] [u e] t [y i] [u y] [u e] [t t] [y i] [u y] [t r] [e r] [e s] [a p] [p o] [s u] [o p] [o i] [u y] [u e] [t t] t [y i] [u y] [u e] t [y i] [u y] [u o] O [6 f] [f d] [s d] s [f f] [d s] d s [f f] [d s] d [f g] [f s] [2 s] [s s] s [p p] [2 s] [s s] s [p p] [3 s] [s s] [p s] [d a] [p s] a [6 f] [f d] [s d] s [f f] [d s] d s [f f] [d s] d [f g] [f s] [2 s] [s s] s [p p] [2 s] [s s] s [p p] [3 s] [s s] [p s] [d a] [f f] [f g] [[6d] s] [8 s] [[4s] s] [p s] [d f] [3 f] [f f] g [[2d] s] [3 s] [[4s] s] [p s] [d a] [5 f] [f f] g [[6d] s] [7 s] [[8s] s] [p s] [d f] [8 f] [f f] g [[4d] s] [5 s] [[8s] s] [p s] [d a] [3 f] [f f] g [[9ed]] [[60s]] [[0wra]] [[680p]]',
			'enter_ninja': '[et] [et] [et] [et] [et] [et] [et] [et] [y] [y] [y] [y] [y] [y] [y] [y] [u] [u] [u] [u] [u] [u] [u] [u] [i] [i] [i] [i] [i] [i] [i] [i] [et] y [u] i [et] y [u] i [et] y [u] i [et] y [u] i [et] [et] [et] [et] [y] [y] [y] [y] [u] [u] [u] [u] [i] [i] [i] [i] [et] y u i [et] y u i [et] y u i [et] y u i [p] [p] [p] [p] [o] [o] [o] [o] [i] [i] [i] [i] [u] [u] [u] [u] [y] [y] [y] [y] [t] [t] [t] [t] [r] [r] [r] [r] [e] [e] [e] [e] [p] o i u y t r e [p] o i u y t r e [p] [o] [i] [u] [y] [t] [r] [e] [p] [o] [i] [u] [y] [t] [r] [e] [et] [y] [u] [i] [et] [y] [u] [i] [et] [y] [u] [i] [et] [y] [u] [i] s d f g h j k l s d f g h j k l [s] [d] [f] [g] [h] [j] [k] [l] [s] [d] [f] [g] [h] [j] [k] [l] [et] y u i [et] y u i [et] y u i [et] y u i p o i u y t r e p o i u y t r e [p] [o] [i] [u] [y] [t] [r] [e] [p] [o] [i] [u] [y] [t] [r] [e] [et] [y] [u] [i] [et] [y] [u] [i] [et] [y] [u] [i] [et] [y] [u] [i] z x c v b n m z x c v b n m [z] [x] [c] [v] [b] [n] [m] [z] [x] [c] [v] [b] [n] [m] [et] y u i [et] y u i [et] y u i [et] y u i',
			'simple': '1 2 3 4 5 - 5 4 3 2 1 - [q w e] - [a s d] - 1 2 3 4 5'
		};
		
		function getServerIP() {
			fetch('/get_ip')
				.then(response => response.json())
				.then(data => {
					let ipText = '';
					data.forEach(ip => {
						ipText += '<div>http://' + ip + ':{{.Port}}</div>';
					});
					document.getElementById('ipAddress').innerHTML = ipText;
				});
		}
		
		function updateDisplay() {
			const delaySec = (currentDelay / 1000).toFixed(3);
			document.getElementById('delayValue').textContent = 'Задержка: ' + delaySec + ' сек';
			document.getElementById('delaySlider').value = currentDelay;
			
			// Обновление прогресса
			const percent = totalNotes > 0 ? Math.round((currentProgress / totalNotes) * 100) : 0;
			document.getElementById('progressValue').textContent = 
				'Прогресс: ' + currentProgress + '/' + totalNotes + ' (' + percent + '%)';
			document.getElementById('progressSlider').value = percent;
		}
		
		function sendDelay() {
			fetch('/set_delay?delay=' + (currentDelay / 1000), { method: 'POST' });
		}
		
		function changeDelay(delta) {
			currentDelay = Math.max(10, Math.min(500, currentDelay + delta));
			updateDisplay();
			sendDelay();
		}
		
		function resetDelay() {
			currentDelay = 100;
			updateDisplay();
			sendDelay();
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
		
		function seekRelative(delta) {
			const currentPercent = parseInt(document.getElementById('progressSlider').value);
			let newPercent = currentPercent + delta;
			if (newPercent < 0) newPercent = 0;
			if (newPercent > 100) newPercent = 100;
			seekToPercent(newPercent);
		}
		
		function seekToStart() {
			seekToPercent(0);
		}
		
		function seekToCurrent() {
			const currentPercent = parseInt(document.getElementById('progressSlider').value);
			seekToPercent(currentPercent);
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
				// После сохранения запрашиваем информацию о последовательности
				fetch('/get_sequence_info')
					.then(response => response.json())
					.then(data => {
						totalNotes = data.total_notes || 0;
						currentProgress = 0;
						updateDisplay();
					});
			});
		}
		
		function loadSequence() {
			fetch('/get_sequence')
				.then(response => response.text())
				.then(text => {
					document.getElementById('sequence').value = text;
					showNotification('Последовательность загружена!');
				});
		}
		
		function clearSequence() {
			document.getElementById('sequence').value = '';
			showNotification('Поле очищено!');
		}
		
		function loadExample(name) {
			if (examples[name]) {
				document.getElementById('sequence').value = examples[name];
				showNotification('Пример ' + name + ' загружен!');
			}
		}
		
		function showNotification(message) {
			const statusEl = document.getElementById('status');
			const originalText = statusEl.textContent;
			statusEl.textContent = message;
			statusEl.style.background = '#d4edda';
			statusEl.style.borderLeftColor = '#28a745';
			
			setTimeout(function() {
				statusEl.textContent = originalText;
				statusEl.style.background = '#e8f5e9';
				statusEl.style.borderLeftColor = '#4CAF50';
			}, 3000);
		}
		
		// Горячие клавиши
		document.addEventListener('keydown', function(e) {
			if (e.key === '+' || e.key === '=') {
				e.preventDefault();
				changeDelay(10);
			} else if (e.key === '-' || e.key === '_') {
				e.preventDefault();
				changeDelay(-10);
			} else if (e.key === ' ') {
				e.preventDefault();
				if (document.getElementById('status').textContent.includes('Пауза')) {
					resume();
				} else {
					pause();
				}
			} else if (e.key === 'Enter') {
				e.preventDefault();
				play();
			} else if (e.key === 'Escape') {
				e.preventDefault();
				stop();
			}
		});
		
		// Инициализация слайдеров
		document.getElementById('delaySlider').addEventListener('input', function(e) {
			currentDelay = parseInt(e.target.value);
			updateDisplay();
			sendDelay();
		});
		
		document.getElementById('progressSlider').addEventListener('input', function(e) {
			const percent = parseInt(e.target.value);
			document.getElementById('progressValue').textContent = 
				'Прогресс: ' + currentProgress + '/' + totalNotes + ' (' + percent + '%)';
		});
		
		document.getElementById('progressSlider').addEventListener('change', function(e) {
			seekToPercent(parseInt(e.target.value));
		});
		
		// Получаем текущий статус каждые секунду
		function updateStatus() {
			fetch('/get_status')
				.then(response => response.json())
				.then(data => {
					// Обновляем состояние кнопок
					if (data.playing) {
						if (data.paused) {
							document.getElementById('status').textContent = '⏸ Пауза на позиции ' + data.position + '/' + data.total_notes;
							document.getElementById('status').style.background = '#fff3cd';
							document.getElementById('status').style.borderLeftColor = '#ffc107';
						} else {
							document.getElementById('status').textContent = '▶ Воспроизведение... ' + data.position + '/' + data.total_notes;
							document.getElementById('status').style.background = '#d4edda';
							document.getElementById('status').style.borderLeftColor = '#28a745';
						}
					} else {
						if (data.position > 0) {
							document.getElementById('status').textContent = '⏹ Остановлено на позиции ' + data.position + '/' + data.total_notes;
						} else {
							document.getElementById('status').textContent = 'Готово';
						}
						document.getElementById('status').style.background = '#e8f5e9';
						document.getElementById('status').style.borderLeftColor = '#4CAF50';
					}
					
					// Обновляем прогресс
					currentProgress = data.position || 0;
					totalNotes = data.total_notes || 0;
					
					// Обновляем задержку
					if (data.delay && data.delay !== currentDelay) {
						currentDelay = Math.round(data.delay * 1000);
						updateDisplay();
					}
					
					updateDisplay();
				})
				.catch(err => {
					console.error('Ошибка получения статуса:', err);
				});
		}
		
		// Получаем IP сервера при загрузке
		window.addEventListener('load', function() {
			getServerIP();
			updateDisplay();
			// Запускаем обновление статуса каждую секунду
			setInterval(updateStatus, 1000);
			// Первое обновление статуса
			setTimeout(updateStatus, 500);
			
			// Запрашиваем информацию о последовательности
			fetch('/get_sequence_info')
				.then(response => response.json())
				.then(data => {
					totalNotes = data.total_notes || 0;
					updateDisplay();
				});
			
			// Устанавливаем начальный фокус на поле ввода
			document.getElementById('sequence').focus();
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

func handleGetIP(w http.ResponseWriter, r *http.Request) {
	ips := getLocalIPs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ips)
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

	// Устанавливаем начальную последовательность (пример)
	setSequence("1 2 3 4 5 - 5 4 3 2 1 - [q w e] - [a s d]")

	// Настраиваем HTTP маршруты
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/set_delay", handleSetDelay)
	http.HandleFunc("/play", handlePlay)
	http.HandleFunc("/pause", handlePause)
	http.HandleFunc("/resume", handleResume)
	http.HandleFunc("/stop", handleStop)
	http.HandleFunc("/seek", handleSeek)
	http.HandleFunc("/set_sequence", handleSetSequence)
	http.HandleFunc("/get_sequence", handleGetSequence)
	http.HandleFunc("/get_status", handleGetStatus)
	http.HandleFunc("/get_sequence_info", handleGetSequenceInfo)
	http.HandleFunc("/get_ip", handleGetIP)

	port := ":5555"
	url := fmt.Sprintf("  http://localhost%s", port)
	defer openBrowser(url)
	fmt.Printf("Сервер запущен на порту %s\n", port)
	fmt.Println("Откройте в браузере:")
	fmt.Println(url)

	// Получаем и выводим локальные IP адреса
	ips := getLocalIPs()
	if len(ips) > 0 {
		fmt.Println("Или с других устройств в сети:")
		for _, ip := range ips {
			fmt.Printf("  http://%s%s\n", ip, port)
		}
	}

	err := http.ListenAndServe(port, nil)
	if err != nil {
		fmt.Printf("Ошибка запуска сервера: %v\n", err)
	}
	defer openBrowser(url)
}
