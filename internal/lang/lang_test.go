package lang

import "testing"

// The samples are lyric-like passages written for this test (short, with
// repeated lines, informal words), not taken from songs.
var samples = map[string][]string{
	"eng": {
		"I woke up this morning and the world was gray\nI walked down the street with nothing to say\nOh baby come back to me\nCome back to me, oh baby, come back",
		"We danced all night under the neon lights\nYou told me it was alright\nNo I don't want to go home tonight\nLet's stay here till the morning light",
		"Don't stop believing in what you've got\nThe road is long but we're not lost\nHold on, hold on, hold on to me\nWe'll find our way across the sea",
	},
	"fra": {
		"Je marche seul dans la rue sous la pluie\nJe pense à toi du matin jusqu'à la nuit\nReviens-moi, reviens, mon amour\nJe t'attendrai encore longtemps",
		"Elle danse dans la lumière de la lune\nOn s'est promis de rester ensemble\nToute la vie, toute la nuit\nRien ne pourra nous séparer",
		"Les jours passent et je ne sais plus\nOù est parti le temps qu'on a perdu\nDis-moi pourquoi tu pars si loin\nJe n'ai plus rien que mes chagrins",
	},
	"spa": {
		"Camino solo por la calle bajo la lluvia\nPienso en ti desde la mañana hasta la noche\nVuelve a mí, vuelve, mi amor\nTe esperaré todavía mucho tiempo",
		"Bailamos toda la noche bajo la luna\nMe prometiste quedarte a mi lado\nPara siempre, para siempre\nNada nos podrá separar",
		"No sé qué hacer con tanto dolor\nTe fuiste lejos sin decirme adiós\nDime por qué, dime por qué\nMi corazón se rompió en dos",
	},
	"deu": {
		"Ich laufe allein durch die Straße im Regen\nIch denke an dich von morgens bis nachts\nKomm zurück zu mir, mein Schatz\nIch werde noch lange auf dich warten",
		"Wir tanzen die ganze Nacht im Mondlicht\nDu hast versprochen, bei mir zu bleiben\nFür immer und ewig\nNichts kann uns trennen",
		"Die Tage vergehen und ich weiß nicht mehr\nWohin die verlorene Zeit gegangen ist\nSag mir, warum gehst du so weit weg\nMein Herz ist schwer und leer",
	},
	"ita": {
		"Cammino da solo per la strada sotto la pioggia\nPenso a te dalla mattina alla sera\nTorna da me, torna, amore mio\nTi aspetterò ancora per tanto tempo",
		"Balliamo tutta la notte sotto la luna\nMi hai promesso di restare con me\nPer sempre, per sempre\nNiente potrà separarci",
	},
	"por": {
		"Caminho sozinho pela rua debaixo da chuva\nPenso em você da manhã até a noite\nVolta pra mim, volta, meu amor\nVou te esperar ainda por muito tempo",
		"Dançamos a noite inteira sob a lua\nVocê prometeu ficar do meu lado\nPara sempre, para sempre\nNada vai nos separar",
	},
	"nld": {
		"Ik loop alleen door de straat in de regen\nIk denk aan jou van de ochtend tot de nacht\nKom terug bij mij, mijn lief\nIk zal nog lang op je wachten",
	},
	"swe": {
		"Jag går ensam på gatan i regnet\nJag tänker på dig från morgon till natt\nKom tillbaka till mig, min älskling\nJag väntar på dig länge än",
	},
	"rus": {
		"Я иду один по улице под дождём\nЯ думаю о тебе с утра до ночи\nВернись ко мне, вернись, любовь моя\nЯ буду ждать тебя ещё долго",
	},
	"jpn": {
		"夜の街を一人で歩いている\n雨の中であなたのことを思い出す\n戻ってきて、愛しい人\nずっとここで待っているよ",
	},
	"kor": {
		"비 오는 거리를 혼자 걷고 있어\n아침부터 밤까지 네 생각뿐이야\n돌아와 줘 내 사랑\n나는 오래오래 기다릴게",
	},
}

// A wrong language is worse than none (it would trigger a false language
// jump), so the detector may answer "und" but must never answer wrongly.
func TestDetectNeverWrong(t *testing.T) {
	total, right, unsure := 0, 0, 0
	for want, texts := range samples {
		for _, s := range texts {
			total++
			switch got, conf := Detect(s); got {
			case want:
				right++
			case Undetermined:
				unsure++
			default:
				t.Errorf("want %s, got %s (%.2f) for %.40q", want, got, conf, s)
			}
		}
	}
	t.Logf("%d samples: %d right, %d undetermined", total, right, unsure)
	if right < total*2/3 {
		t.Errorf("only %d of %d detected", right, total)
	}
}

func TestDetectUndetermined(t *testing.T) {
	for _, s := range []string{"", "la la la", "Instrumental", "1 2 3 4 5 6 7 8 9"} {
		if got, _ := Detect(s); got != Undetermined {
			t.Errorf("Detect(%q) = %s, want %s", s, got, Undetermined)
		}
	}
}

func TestName(t *testing.T) {
	if Name("fra") != "French" || Name(Undetermined) != "undetermined" || Name("xyz") != "xyz" {
		t.Errorf("names: %q %q %q", Name("fra"), Name(Undetermined), Name("xyz"))
	}
	if Known("") || Known(Undetermined) || !Known("eng") {
		t.Error("Known")
	}
}
