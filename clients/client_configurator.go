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
	"io/ioutil"
	"os"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	termbox "github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/cluster/configurator"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/server"
	"github.com/signal18/replication-manager/utils/dbhelper"
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
var coresInput string
var connectionsInput string
var userInput string
var userInput2 string
var userInput3 string
var userInput4 string
var inputMode bool = false
var inputMode2 bool = false
var inputMode3 bool = false
var inputMode4 bool = false
var cursorPos int = 0
var cursorPos2 int = 0
var cursorPos3 int = 0
var cursorPos4 int = 0
var RepMan *server.ReplicationManager
var addedTags = make(map[string]bool)

const maxTagsInView = 5          // Nombre maximum de tags à afficher à la fois
var startViewIndex = 0           // Index de début de la fenêtre de visualisation
var endViewIndex = maxTagsInView // Index de fin de la fenêtre de visualisation, initialement réglé sur maxTagsInView

var configuratorCmd = &cobra.Command{
	Use:   "configurator",
	Short: "Config generator",
	Long:  `Config generator produce tar.gz for databases and proxies based on ressource and tags description`,
	Run: func(cmd *cobra.Command, args []string) {
		conf.WithEmbed = WithEmbed
		RepMan = new(server.ReplicationManager)
		RepMan.InitConfig(conf)
		go RepMan.Run()
		time.Sleep(2 * time.Second)
		cluster:= RepMan.Clusters[RepMan.ClusterList[0]]
		if cluster == nil  {
			log.Fatalf("No Cluster found .replication-manager/config.toml")
		 }
		for _ , s := range cluster.Servers 	 {
			conn , err := s.GetNewDBConn()
			if err !=nil  {
					log.WithError(err).Fatalf("Connecting error to database in .replication-manager/config.toml: %s", s.URL)
			}
			variables, _, err := dbhelper.GetVariablesCase(conn, s.DBVersion,"LOWER")
			if err !=nil  {
					log.WithError(err).Fatalf("Get variables failed %s", s.URL)
			}

			log.Infof("datadir %s",  variables["DATADIR"])
		}
		RepMan.Clusters["mycluster"].WaitDatabaseCanConn()
		//	var conf config.Config
		var configurator configurator.Configurator
		configurator.Init(conf)

		for _ , server := range cluster.Servers 	 {
			err := configurator.GenerateDatabaseConfig(server.Datadir, cluster.Conf.WorkingDir,  server.GetVariablesCaseSensitive()["DATADIR"], server.GetEnv())
			if err !=nil  {
					log.WithError(err).Fatalf("Generate database config failed %s", server.URL)
			}
			log.Infof("Generate database config datadir %s/config.tar.gz", server.Datadir)
		}

		dbCategories = configurator.GetDBModuleCategories()
		dbCategoriesSortedKeys = make([]string, 0, len(dbCategories))
		for k := range dbCategories {
			dbCategoriesSortedKeys = append(dbCategoriesSortedKeys, k)
		}

		sort.Strings(dbCategoriesSortedKeys)
		defaultTags := configurator.GetDBTags()
		for _, v := range defaultTags {

			addedTags[v] = true
			//fmt.Printf("%s \n" ,v)
		}
		//os.Exit(3)

		//default
		userInput = configurator.GetConfigDBMemory()
		memoryInput = configurator.GetConfigDBMemory()

		userInput2 = configurator.GetConfigDBDiskIOPS()
		ioDiskInput = configurator.GetConfigDBDiskIOPS()

		userInput3 = configurator.GetConfigDBCores()
		coresInput = configurator.GetConfigDBCores()

		userInput4 = configurator.GetConfigMaxConnections()
		connectionsInput = configurator.GetConfigMaxConnections()

		fmt.Printf("%s \n", RepMan.Clusters["mycluster"].Conf.ProvTags)
		conf.SetLogOutput(ioutil.Discard)
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


		cliDisplayConfigurator(&configurator)

		for cliExit == false {
			select {
			case <-ticker.C:
				cliDisplayConfigurator(&configurator)

			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {

						log.Infof("CtrlS")

						cliExit = true
					}

					if event.Key == termbox.KeyArrowLeft {
						switch PanIndex {
						case 0:
							dbCategoryIndex--
							dbTagIndex = 0
							if dbCategoryIndex < 0 {
								dbCategoryIndex = len(dbCategoriesSortedKeys) - 1
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
						default:
						}
					}

					if event.Key == termbox.KeyArrowDown {
						switch PanIndex {
						case 0:
							PanIndex = 2
						case 1:
							if dbTagIndex < len(dbCurrentCategoryTags)-1 {
								dbTagIndex++
								if dbTagIndex >= endViewIndex && endViewIndex < len(dbCurrentCategoryTags) {
									// Faites défiler la fenêtre de visualisation vers le bas
									startViewIndex++
									endViewIndex++
								}
							}
						case 2:
							PanIndex = 3
						case 3:
							PanIndex = 4
						case 4:
							PanIndex = 5
						case 5:
							PanIndex = 0
						default:
						}
					}

					if event.Key == termbox.KeyArrowUp {
						switch PanIndex {
						case 0:
							PanIndex = 5
						case 1:
							if dbTagIndex > 0 {
								dbTagIndex--
								if dbTagIndex < startViewIndex && startViewIndex > 0 {
									// Faites défiler la fenêtre de visualisation vers le haut
									startViewIndex--
									endViewIndex--
								}
							}
						case 2:
							PanIndex = 0
						case 3:
							PanIndex = 2
						case 4:
							PanIndex = 3
						case 5:
							PanIndex = 4
						default:
						}
					}

					if event.Key == termbox.KeyEnter {
						switch PanIndex {
						case 0:
							PanIndex = 1
						case 1:
							if addedTags[dbCurrrentTag] {
								configurator.DropDBTag(dbCurrrentTag)
								addedTags[dbCurrrentTag] = false
							} else {
								configurator.AddDBTag(dbCurrrentTag)
								addedTags[dbCurrrentTag] = true
							}
						case 2:
							if inputMode {
								// L'utilisateur a terminé la saisie, appuyez sur Entrée pour soumettre
								inputMode = false
								memoryInput = userInput
							} else {
								// Activez le mode de saisie
								inputMode = true
							}

						case 3:
							if inputMode2 {
								// L'utilisateur a terminé la saisie, appuyez sur Entrée pour soumettre
								inputMode2 = false
								ioDiskInput = userInput2
							} else {
								// Activez le mode de saisie
								inputMode2 = true
							}
						case 4:
							if inputMode3 {
								// L'utilisateur a terminé la saisie, appuyez sur Entrée pour soumettre
								inputMode3 = false
								coresInput = userInput3
							} else {
								// Activez le mode de saisie
								inputMode3 = true
							}
						case 5:
							if inputMode4 {
								// L'utilisateur a terminé la saisie, appuyez sur Entrée pour soumettre
								inputMode4 = false
								connectionsInput = userInput4
							} else {
								// Activez le mode de saisie
								inputMode4 = true
							}
						default:
						}
					}

					if inputMode || inputMode2 || inputMode3 || inputMode4 {
						// Gérer la saisie de l'utilisateur dans le mode de saisie
						if event.Ch != 0 && event.Ch >= '0' && event.Ch <= '9' { // Vérifier si le caractère est un chiffre
							// Ajouter un nouveau caractère à la position du curseur
							switch PanIndex {
							case 2:
								if len(userInput) < 6 {
									userInput = userInput[:cursorPos] + string(event.Ch) + userInput[cursorPos:]
									cursorPos++
								}
							case 3:
								if len(userInput2) < 6 {
									userInput2 = userInput2[:cursorPos2] + string(event.Ch) + userInput2[cursorPos2:]
									cursorPos2++
								}
							case 4:
								if len(userInput3) < 6 {
									userInput3 = userInput3[:cursorPos3] + string(event.Ch) + userInput3[cursorPos3:]
									cursorPos3++
								}
							case 5:
								if len(userInput4) < 6 {
									userInput4 = userInput4[:cursorPos4] + string(event.Ch) + userInput4[cursorPos4:]
									cursorPos4++
								}
							default:
							}
						}

						if event.Key == termbox.KeyBackspace || event.Key == termbox.KeyBackspace2 {
							switch PanIndex {
							case 2:
								if cursorPos > 0 && len(userInput) > 0 {
									// Supprimer le caractère à gauche du curseur
									userInput = userInput[:cursorPos-1] + userInput[cursorPos:]
									cursorPos--
								}
							case 3:
								if cursorPos2 > 0 && len(userInput2) > 0 {
									// Supprimer le caractère à gauche du curseur
									userInput2 = userInput2[:cursorPos2-1] + userInput2[cursorPos2:]
									cursorPos2--
								}
							case 4:
								if cursorPos3 > 0 && len(userInput3) > 0 {
									// Supprimer le caractère à gauche du curseur
									userInput3 = userInput3[:cursorPos3-1] + userInput3[cursorPos3:]
									cursorPos3--
								}
							case 5:
								if cursorPos4 > 0 && len(userInput4) > 0 {
									// Supprimer le caractère à gauche du curseur
									userInput4 = userInput4[:cursorPos4-1] + userInput4[cursorPos4:]
									cursorPos4--
								}
							default:
							}

						}

						if event.Key == termbox.KeyArrowLeft {
							switch PanIndex {
							case 2:
								if cursorPos > 0 {
									// Déplacer le curseur vers la gauche
									cursorPos--
								}
							case 3:
								if cursorPos2 > 0 {
									// Déplacer le curseur vers la gauche
									cursorPos2--
								}
							case 4:
								if cursorPos3 > 0 {
									// Déplacer le curseur vers la gauche
									cursorPos3--
								}
							case 5:
								if cursorPos4 > 0 {
									// Déplacer le curseur vers la gauche
									cursorPos4--
								}
							default:
							}
						}

						if event.Key == termbox.KeyArrowRight {
							switch PanIndex {
							case 2:
								if cursorPos < len(userInput) {
									// Déplacer le curseur vers la droite
									cursorPos++
								}
							case 3:
								if cursorPos2 < len(userInput2) {
									// Déplacer le curseur vers la droite
									cursorPos2++
								}
							case 4:
								if cursorPos3 < len(userInput3) {
									// Déplacer le curseur vers la droite
									cursorPos3++
								}
							case 5:
								if cursorPos4 < len(userInput4) {
									// Déplacer le curseur vers la droite
									cursorPos4++
								}
							default:
							}
						}
					}

					if event.Key == termbox.KeyEsc {
						switch PanIndex {
						case 1:
							dbTagIndex = 0
							startViewIndex = 0
							endViewIndex = maxTagsInView
							PanIndex = 0
						default:
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
			//	case 's':
			//		termbox.Sync()
				default:
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
		RepMan.Stop()
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

	//PanIndex = 0 -- TAGS
	colorCell := termbox.ColorWhite
	if PanIndex == 0 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliPrintTb(1, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, "TAGS :")
	curWitdh := len("TAGS :") + 2

	for i, cat := range dbCategoriesSortedKeys {
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
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliTlog.Line++

	//PanIndex = 1 -- print available tags for a category
	if PanIndex == 1 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	curWitdh = 1

	dbCurrentCategoryTags = make([]v3.Tag, 0, len(tags))
	dbUsedTags = configurator.GetDBTags()

	for _, tag := range tags {
		if dbCurrrentCategory == tag.Category /*&& !configurator.HaveDBTag(tag.Name)*/ {
			dbCurrentCategoryTags = append(dbCurrentCategoryTags, tag)
		}
	}

	for i := startViewIndex; i < endViewIndex && i < len(dbCurrentCategoryTags); i++ {
		tag := dbCurrentCategoryTags[i]
		var tagDisplay string
		if addedTags[tag.Name] {
			tagDisplay = "[X] " + tag.Name
		} else {
			tagDisplay = "[ ] " + tag.Name
		}
		if i == dbTagIndex && PanIndex == 1 {
			cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorBlack, colorCell, tagDisplay)
			dbCurrrentTag = tag.Name
		} else {
			cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, tagDisplay)
		}
		cliTlog.Line++
	}

	//PanIndex 2 ou plus
	if PanIndex == 2 || PanIndex == 3 || PanIndex == 4 || PanIndex == 5 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliPrintTb(1, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, "RESSOURCES :")
	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliTlog.Line++

	// Déterminez la largeur maximale des étiquettes
	labels := []string{"MEMORY :", "IO DISK :", "CORES :", "CONNECTIONS :"}
	maxLabelWidth := 0
	for _, label := range labels {
		if len(label) > maxLabelWidth {
			maxLabelWidth = len(label)
		}
	}

	// Ajouter un peu d'espace entre l'étiquette et l'entrée
	labelPadding := 2

	if PanIndex == 2 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "MEMORY")
	if !inputMode {
		formattedInput := formatInput(userInput, cursorPos, inputMode)
		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, formattedInput)
	}
	// Si nous sommes en mode de saisie, affichez aussi ce que l'utilisateur a saisi jusqu'à présent
	if inputMode && PanIndex == 2 {
		cliPrintTb(1, cliTlog.Line, termbox.ColorBlack, colorCell, "MEMORY")
		// Afficher la saisie de l'utilisateur avec le curseur
		formattedInput := formatInput(userInput, cursorPos, inputMode)
		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, formattedInput)
		cliTlog.Line++
	}
	cliTlog.Line++
	cliTlog.Line++

	if PanIndex == 3 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "IO DISK")
	if !inputMode2 {
		formattedInput2 := formatInput(userInput2, cursorPos2, inputMode2)
		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, formattedInput2)
	}
	// Si nous sommes en mode de saisie, affichez aussi ce que l'utilisateur a saisi jusqu'à présent
	if inputMode2 && PanIndex == 3 {
		cliPrintTb(1, cliTlog.Line, termbox.ColorBlack, colorCell, "IO DISK")
		// Afficher la saisie de l'utilisateur avec le curseur
		formattedInput2 := formatInput(userInput2, cursorPos2, inputMode2)
		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, formattedInput2)
		cliTlog.Line++
	}

	cliTlog.Line++
	cliTlog.Line++

	if PanIndex == 4 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "CORES")
	if !inputMode3 {
		formattedInput3 := formatInput(userInput3, cursorPos3, inputMode3)

		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, formattedInput3)
	}
	// Si nous sommes en mode de saisie, affichez aussi ce que l'utilisateur a saisi jusqu'à présent
	if inputMode3 && PanIndex == 4 {
		cliPrintTb(1, cliTlog.Line, termbox.ColorBlack, colorCell, "CORES")
		// Afficher la saisie de l'utilisateur avec le curseur
		formattedInput3 := formatInput(userInput3, cursorPos3, inputMode3)
		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, formattedInput3)
		cliTlog.Line++
	}

	cliTlog.Line++
	cliTlog.Line++

	if PanIndex == 5 {
		colorCell = termbox.ColorCyan
	} else {
		colorCell = termbox.ColorWhite
	}

	cliPrintTb(1, cliTlog.Line, colorCell, termbox.ColorBlack, "CONNECTIONS")
	if !inputMode4 {
		formattedInput4 := formatInput(userInput4, cursorPos4, inputMode4)
		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, formattedInput4)
	}

	// Si nous sommes en mode de saisie, affichez aussi ce que l'utilisateur a saisi jusqu'à présent
	if inputMode4 && PanIndex == 5 {
		cliPrintTb(1, cliTlog.Line, termbox.ColorBlack, colorCell, "CONNECTIONS")
		// Afficher la saisie de l'utilisateur avec le curseur
		formattedInput4 := formatInput(userInput4, cursorPos4, inputMode4)
		cliPrintTb(maxLabelWidth+labelPadding+1, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, formattedInput4)
		cliTlog.Line++
	}

	cliTlog.Line++
	cliTlog.Line++
	cliTlog.Line++
	cliPrintTb(0, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q Quit, Ctrl-S Save, Arrows to navigate, Enter to select, Esc to exit")

	cliTlog.Line = cliTlog.Line + 3
	cliTlog.Print()

	termbox.Flush()

}

func formatInput(input string, cursorPos int, editing bool) string {
	// Formater l'entrée pour qu'elle soit toujours de 6 caractères de large
	formattedInput := fmt.Sprintf("%-6s", input)
	if cursorPos > len(formattedInput) {
		cursorPos = len(formattedInput)
	}
	// Ajouter le curseur à l'endroit approprié si l'utilisateur est en train d'éditer
	if editing {
		return "[" + formattedInput[:cursorPos] + "|" + formattedInput[cursorPos:] + "]"
	} else {
		return "[" + formattedInput + "]"
	}
}
