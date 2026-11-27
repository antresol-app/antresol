package library

import "strings"

// genericWords only number or label a piece of a recording: a chapter
// title made of them (and numbers) tells the listener nothing.
var genericWords = wordSet(`part chapter chap ch track disc disk cd file section side audiobook audio book unknown untitled
	one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty
	часть ч глава гл трек диск файл раздел дорожка запись сторона аудиокнига аудио без названия двадцать
	первая вторая третья четвертая четвёртая пятая шестая седьмая восьмая девятая десятая одиннадцатая двенадцатая тринадцатая
	четырнадцатая пятнадцатая шестнадцатая семнадцатая восемнадцатая девятнадцатая двадцатая
	первый второй третий четвертый четвёртый пятый шестой седьмой восьмой девятый десятый`)

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(s) {
		out[w] = true
	}
	return out
}
