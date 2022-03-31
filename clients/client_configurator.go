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
	"time"
	"sort"
	"strings"

	termbox "github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	v3 "github.com/signal18/replication-manager/repmanv3"
)

 var dbCurrrentTag string
 var dbCurrrentCategory string
 var dbCategories map [string]string
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
		interval := time.Second
		ticker := time.NewTicker(interval * time.Duration(2))
		var conf config.Config
		var configurator configurator.Configurator
  	configurator.Init(conf)
  	dbCategories=configurator.GetDBModuleCategories()
		dbCategoriesSortedKeys = make([]string, 0, len(dbCategories))
  	for k := range dbCategories {
	  	dbCategoriesSortedKeys = append(dbCategoriesSortedKeys, k)
  	}
		sort.Strings(dbCategoriesSortedKeys)

		for cliExit == false {
			select {
			case <-ticker.C:

				cliDisplayConfigurator(&configurator)
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {

					}

					if event.Key == termbox.KeyArrowLeft  {
						switch	PanIndex{
					  case 0:
						dbCategoryIndex--
						dbTagIndex=0
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
							dbUsedTagIndex = len(dbUsedTags)  - 1
						}

					default:
					}

					}
					if event.Key == termbox.KeyArrowRight{

					 switch	PanIndex{
					 case 0:
					  dbCategoryIndex++
					  dbTagIndex=0
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
						if PanIndex >=3 {
							PanIndex = 0
					  }
					}
					if event.Key == termbox.KeyArrowUp {
							PanIndex--
							if PanIndex < 0 {
						 		PanIndex = 2
						 	}
					}
					if event.Key == termbox.KeyEnter {
						switch	PanIndex {
							case 1:
								configurator.AddDBTag(dbCurrrentTag)
							case 2:
							  configurator.DropDBTag(dbCurrrentTag)
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
				case 's':
					termbox.Sync()
				}
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
  tableau:="─"
	tags := configurator.GetDBModuleTags()
	width , _ := termbox.Size()

	colorCell := termbox.ColorWhite
	if PanIndex==0 {
		colorCell =	termbox.ColorCyan
	} else {
	  colorCell =	termbox.ColorWhite
	}
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
	cliTlog.Line++
	cliPrintTb(1, cliTlog.Line,  colorCell,termbox.ColorBlack, "CONFIG CATEGORY")
	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
  cliTlog.Line++

	curWitdh :=1

	for i, cat := range dbCategoriesSortedKeys {
		//cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack,  "toto ")
		tag:=dbCategories[cat]
		if dbCurrrentCategory == "" ||  i==dbCategoryIndex {
			dbCurrrentCategory=cat
			if dbCurrrentTag == "" {
				dbCurrrentTag=tag
			}
		}

		if curWitdh > width {
			curWitdh =1
			cliTlog.Line++
		}
		if dbCurrrentCategory !=cat  {
				cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, strings.ToUpper(cat))
		}	else {
				cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorBlack, colorCell, strings.ToUpper(cat) )
		}
		curWitdh += len(cat)
		curWitdh++

	}
 	cliTlog.Line++
  cliTlog.Line++

	// print available tags for a category
	if PanIndex==1 {
		colorCell =	termbox.ColorCyan
	} else {
		colorCell =	termbox.ColorWhite
	}
	cliPrintfTb(0,cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
  cliTlog.Line++
	cliPrintTb(1, cliTlog.Line, colorCell,termbox.ColorBlack, "CONFIG AVAILABLE")
	cliTlog.Line++
	cliPrintfTb(0, cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
  cliTlog.Line++
	curWitdh =1

	dbCurrentCategoryTags = make([]v3.Tag, 0, len(tags))



	for _, tag := range tags {
    	if dbCurrrentCategory == tag.Category && !configurator.HaveDBTag(tag.Name) {
				dbCurrentCategoryTags = append(dbCurrentCategoryTags, tag)
			}
	}

	for i, tag := range dbCurrentCategoryTags {
		if dbCurrrentCategory == tag.Category {

			if curWitdh > width {
					curWitdh =2
					cliTlog.Line++
			}
			if 	dbTagIndex!=(i) || PanIndex!=1  {
					cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, tag.Name )
			}	else {
					cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorBlack, colorCell, tag.Name)
					dbCurrrentTag=tag.Name
			}
			curWitdh +=len(tag.Name)
			curWitdh++
	  }

		}
	 cliTlog.Line++
	 cliTlog.Line++

	 //print used tags
	 if PanIndex==2 {
 		colorCell =	termbox.ColorCyan
 	} else {
 	  colorCell =	termbox.ColorWhite
 	}
	 cliPrintfTb(0,cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
 	 cliTlog.Line++
	 cliPrintTb(1, cliTlog.Line,colorCell,termbox.ColorBlack, "CONFIG TO GENERATE")
	 cliTlog.Line++
	 cliPrintfTb(0,cliTlog.Line, colorCell|termbox.AttrBold, termbox.ColorBlack, "%s", strings.Repeat(tableau, width))
   cliTlog.Line++

	 dbUsedTags = configurator.GetDBTags()
   curWitdh =1
	 for i, tag := range dbUsedTags {

 			if curWitdh > width {
 					curWitdh =2
 					cliTlog.Line++
 			}
 			if 	dbUsedTagIndex!=(i)  || PanIndex!=2 {
					cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, tag )
 			}	else {
 					cliPrintTb(curWitdh, cliTlog.Line, termbox.ColorWhite, colorCell, tag)
					if PanIndex==2 {
					dbCurrrentTag=tag
				}
			}
 			curWitdh +=len(tag)
 			curWitdh++


 		}

	 cliTlog.Line++
	 cliTlog.Line++
	 cliPrintTb(0, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q Quit, Ctrl-S Save, Arrows to navigate, Enter to select")

	cliTlog.Line = cliTlog.Line + 3
	cliTlog.Print()

	termbox.Flush()

}
