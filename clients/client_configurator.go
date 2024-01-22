//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package clients

import (
	"fmt"
	"os"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	termbox "github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var dbCurrrentTag string
var dbCurrrentCategory string
var dbCategories map[string]string
var dbCategoriesSortedKeys []string
var dbUsedTags []string
var dbCategoryIndex int
var dbTagIndex int
var dbCurrentCategoryTags []v3.Tag
var dbUsedTagIndex int
var PanIndex int
var dbHost string
var dbUser string
var dbPassword string
var memoryInput string
var ioDiskInput string
var userInput string
var userInput2 string
var inputMode bool = false
var inputMode2 bool = false
var cursorPos int = 0
var cursorPos2 int = 0

var configuratorCmd = &cobra.Command{
	Use:   "configurator",
	Short: "Config generator",
	Long:  `Config generator produce tar.gz for databases and proxies based on ressource and tags description`,
	Run: func(cmd *cobra.Command, args []string) {
		err := termbox.Init()
		if err != nil {
			log.WithError(err).Fatal("Termbox initialization error")
		}
		_, cliTermlength = termbox.Size()
		if cliTermlength == 0 {
			cliTermlength = 120
		} else if cliTermlength < 18 {
			log.Fatal("Terminal too small, please increase window size")
		}
		termboxChan := cliNewTbChan()
		interval := time.Millisecond
		ticker := time.NewTicker(interval * time.Duration(20))
		var conf config.Config
		var configurator configurator.Configurator
		configurator.Init(conf)
		dbCategories = configurator.GetDBModuleCategories()
		dbCategoriesSortedKeys = make([]string, 0, len(dbCategories))
		for k := range dbCategories {
			dbCategoriesSortedKeys = append(dbCategoriesSortedKeys, k)
		}
		sort.Strings(dbCategoriesSortedKeys)
		cliDisplayConfigurator(&configurator)

		for cliExit == false {
			select {
			case <-ticker.C:
				cliDisplayConfigurator(&configurator)

			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {

					}

					if event.Key == termbox.KeyArrowLeft {
						switch PanIndex {
						case 0:
							dbCategoryIndex--
							dbTagIndex = 0
							if dbCategoryIndex < 0 {
								dbCategoryIndex = len(dbCategoriesSortedKeys) - 1
							}
						case 1:
							dbTagIndex--
							if dbTagIndex < 0 {
								dbTagIndex = len(dbCurrentCategoryTags) - 1
							}
						case 2:
							dbUsedTagIndex--
							if dbUsedTagIndex < 0 {
								dbUsedTagIndex = len(dbUsedTags) - 1
							}

						default:
						}

					}
					if event.Key == termbox.KeyArrowRight {

						switch PanIndex {
						case 0:
							dbCategoryIndex++
							dbTagIndex = 0
							if dbCategoryIndex >= len(dbCategoriesSortedKeys) {
								dbCategoryIndex = 0
							}
						case 1:
							dbTagIndex++
							if dbTagIndex >= len(dbCurrentCategoryTags) {
								dbTagIndex = 0
							}
						case 2:
							dbUsedTagIndex++
							if dbUsedTagIndex >= len(dbUsedTags) {
								dbUsedTagIndex = 0
							}

						default:
						}
					}
					if event.Key == termbox.KeyArrowDown {
						PanIndex++
						if PanIndex >= 5 {
							PanIndex = 0
						}
					}
					if event.Key == termbox.KeyArrowUp {
						PanIndex--
						if PanIndex < 0 {
							PanIndex = 4
						}
					}
					if event.Key == termbox.KeyEnter {
						switch PanIndex {
						case 1:
							configurator.AddDBTag(dbCurrrentTag)
						case 2:
							configurator.DropDBTag(dbCurrrentTag)
						case 3:
							if inputMode {
								// L'utilisateur a terminé la saisie, appuyez sur Entrée pour soumettre
								inputMode = false
								memoryInput = userInput
							} else {
								// Activez le mode de saisie
								inputMode = true
							}

						case 4:
							if inputMode2 {
								// L'utilisateur a terminé la saisie, appuyez sur Entrée pour soumettre
								inputMode2 = false
								ioDiskInput = userInput2
							} else {
								// Activez le mode de saisie
								inputMode2 = true
							}
						default:
						}
					} else if inputMode || inputMode2 {
						// Gérer la saisie de l'utilisateur dans le mode de saisie
						if event.Ch != 0 && event.Ch >= '0' && event.Ch <= '9' { // Vérifier si le caractère est un chiffre
							// Ajouter un nouveau caractère à la position du curseur
							switch PanIndex {
							case 3:
								userInput = userInput[:cursorPos] + string(event.Ch) + userInput[cursorPos:]
								cursorPos++
							case 4:
								userInput2 = userInput2[:cursorPos2] + string(event.Ch) + userInput2[cursorPos2:]
								cursorPos2++
							}
						}
						if event.Key == termbox.KeyBackspace || event.Key == termbox.KeyBackspace2 {
							switch PanIndex {
							case 3:
								if cursorPos > 0 && len(userInput) > 0 {
									// Supprimer le caractère à gauche du curseur
									userInput = userInput[:cursorPos-1] + userInput[cursorPos:]
									cursorPos--
								}
							case 4:
								if cursorPos2 > 0 && len(userInput2) > 0 {
									// Supprimer le caractère à gauche du curseur
									userInput2 = userInput2[:cursorPos2-1] + userInput2[cursorPos2:]
									cursorPos2--
								}
							}

						}
						if event.Key == termbox.KeyArrowLeft {
							if cursorPos > 0 {
								// Déplacer le curseur vers la gauche
								cursorPos--
							}
							if cursorPos2 > 0 {
								// Déplacer le curseur vers la gauche
								cursorPos2--
							}
						}
						if event.Key == termbox.KeyArrowRight {
							if cursorPos < len(userInput) {
								// Déplacer le curseur vers la droite
								cursorPos++
							}
							if cursorPos2 < len(userInput2) {
								// Déplacer le curseur vers la droite
								cursorPos2++
							}

						}
					}

					if event.Key == termbox.KeyCtrlH {
						cliDisplayHelp()
					}
					if event.Key == termbox.KeyCtrlQ {
						cliExit = true
					}
					if event.Key == termbox.KeyCtrlC {
						cliExit = true
					}
				}
				switch event.Ch {
				case 's':
					termbox.Sync()
				}
				cliDisplayConfigurator(&configurator)

			}
		}

	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		termbox.Close()
		if memprofile != "" {
			f, err := os.Create(memprofile)
			if err != nil {
				log.Fatal(err)
			}
			pprof.WriteHeapProfile(f)
			f.Close()
		}
	},
}

func cliDisplayConfigurator(configurator *configurator.Configurator) {

	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	headstr := fmt.Sprintf(" Signal18 Replication Manager Configurator")

	cliPrintfTb(0, 0, termbox.ColorWhite, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, headstr)
	cliPrintfTb(0, 1, termbox.ColorRed, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, cliConfirm)
	cliTlog.Line = 3
	tableau := "─"
	tags := configurator.GetDBModuleTags()
	width, _ := termbox.Size()

	colorCell := termbox.ColorWhite
	if PanIndex == 0 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "CONFIG CATEGORY")
	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++

	curWitdh := 1

	for i, cat := range dbCategoriesSortedKeys {
		//cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack,  "toto ")
		tag := dbCategories[cat]
		if dbCurrrentCategory == "" || i == dbCategoryIndex {
			dbCurrrentCategory = cat
			if dbCurrrentTag == "" {
				dbCurrrentTag = tag
			}
		}

		if curWitdh > width {
			curWitdh = 1
			cliTlog.Line++
		}
		if dbCurrrentCategory != cat {
			cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, strings.ToUpper(cat))
		} else {
			cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorBlack, colorCell, strings.ToUpper(cat))
		}
		curWitdh += len(cat)
		curWitdh++

	}
	cliTlog.Line++
	cliTlog.Line++

	// print available tags for a category
	if PanIndex == 1 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "CONFIG AVAILABLE")
	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	curWitdh = 1

	dbCurrentCategoryTags = make([]v3.Tag, 0, len(tags))

	for _, tag := range tags {
		if dbCurrrentCategory == tag.Category && !configurator.HaveDBTag(tag.Name) {
			dbCurrentCategoryTags = append(dbCurrentCategoryTags, tag)
		}
	}

	for i, tag := range dbCurrentCategoryTags {
		if dbCurrrentCategory == tag.Category {

			if curWitdh > width {
				curWitdh = 2
				cliTlog.Line++
			}
			if dbTagIndex != (i) || PanIndex != 1 {
				cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, tag.Name)
			} else {
				cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorBlack, colorCell, tag.Name)
				dbCurrrentTag = tag.Name
			}
			curWitdh += len(tag.Name)
			curWitdh++
		}

	}
	cliTlog.Line++
	cliTlog.Line++

	//print used tags
	if PanIndex == 2 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "CONFIG TO GENERATE")
	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++

	dbUsedTags = configurator.GetDBTags()
	curWitdh = 1
	for i, tag := range dbUsedTags {

		if curWitdh > width {
			curWitdh = 2
			cliTlog.Line++
		}
		if dbUsedTagIndex != (i) || PanIndex != 2 {
			cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, tag)
		} else {
			cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, colorCell, tag)
			if PanIndex == 2 {
				dbCurrrentTag = tag
			}
		}
		curWitdh += len(tag)
		curWitdh++

	}

	//Marie
	if PanIndex == 3 || PanIndex == 4 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++

	if PanIndex == 3 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "MEMORY : ")
	if !inputMode {
		cliPrintTb(1+len("MEMORY :"), cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, memoryInput)
	}
	// Si nous sommes en mode de saisie, affichez aussi ce que l'utilisateur a saisi jusqu'à présent
	if inputMode && PanIndex == 3 {
		cliPrintTb(1, cliTlog.Line, termbox.ColorBlack, colorCell, "MEMORY :")
		// Afficher la saisie de l'utilisateur avec le curseur
		displayInput := userInput[:cursorPos] + "|" + userInput[cursorPos:]
		cliPrintTb(len("MEMORY : "), cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, displayInput)
		cliTlog.Line++
	}

	cliTlog.Line++
	cliTlog.Line++

	if PanIndex == 4 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "IO DISK : ")
	if !inputMode2 {
		cliPrintTb(1+len("IO DISK :"), cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, ioDiskInput)
	}
	// Si nous sommes en mode de saisie, affichez aussi ce que l'utilisateur a saisi jusqu'à présent
	if inputMode2 && PanIndex == 4 {
		cliPrintTb(1, cliTlog.Line, termbox.ColorBlack, colorCell, "IO DISK :")
		// Afficher la saisie de l'utilisateur avec le curseur
		displayInput2 := userInput2[:cursorPos2] + "|" + userInput2[cursorPos2:]
		cliPrintTb(len("IO DISK : "), cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, displayInput2)
		cliTlog.Line++
	}

	cliTlog.Line++
	cliTlog.Line++
	cliPrintTb(0, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q Quit, Ctrl-S Save, Arrows to navigate, Enter to select")

	cliTlog.Line = cliTlog.Line + 3
	cliTlog.Print()

	termbox.Flush()

}
