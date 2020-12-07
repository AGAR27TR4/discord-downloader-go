package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/fatih/color"
	"github.com/hako/durafmt"
	"mvdan.cc/xurls/v2"
)

type fileItem struct {
	Link     string
	Filename string
	Time     time.Time
}

var (
	skipCommands = []string{
		"skip",
		"ignore",
		"don't save",
		"no save",
	}
)

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	handleMessage(m.Message, false)
}

func messageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if m.EditedTimestamp != discordgo.Timestamp("") {
		handleMessage(m.Message, true)
	}
}

func handleMessage(m *discordgo.Message, edited bool) {
	if !isChannelRegistered(m.ChannelID) {
		return
	}
	channelConfig := getChannelConfig(m.ChannelID)

	// Ignore own messages unless told not to
	if m.Author.ID == user.ID && !config.ScanOwnMessages {
		return
	}
	// Ignore if told so by config
	if !*channelConfig.Enabled || (edited && !*channelConfig.ScanEdits) {
		return
	}

	// Log
	var sendLabel string
	if config.DebugOutput {
		sendLabel = fmt.Sprintf("%s/%s/%s", m.GuildID, m.ChannelID, m.Author.ID)
	} else {
		sendLabel = fmt.Sprintf("%s in \"%s\"#%s", getUserIdentifier(*m.Author), getGuildName(m.GuildID), getChannelName(m.ChannelID))
	}
	content := m.Content
	if len(m.Attachments) > 0 {
		content = content + fmt.Sprintf(" (%d attachments)", len(m.Attachments))
	}
	if edited {
		log.Println(color.CyanString("Edited Message [%s]: %s", sendLabel, content))
	} else {
		log.Println(color.CyanString("Message [%s]: %s", sendLabel, content))
	}

	// User Whitelisting
	if !*channelConfig.UsersAllWhitelisted && channelConfig.UserWhitelist != nil {
		if !stringInSlice(m.Author.ID, *channelConfig.UserWhitelist) {
			log.Println(color.HiYellowString("Message handling skipped due to user not being whitelisted."))
			return
		}
	}
	// User Blacklisting
	if channelConfig.UserBlacklist != nil {
		if stringInSlice(m.Author.ID, *channelConfig.UserBlacklist) {
			log.Println(color.HiYellowString("Message handling skipped due to user being blacklisted."))
			return
		}
	}

	// Skipping
	canSkip := config.AllowSkipping
	if channelConfig.OverwriteAllowSkipping != nil {
		canSkip = *channelConfig.OverwriteAllowSkipping
	}
	if canSkip {
		for _, cmd := range skipCommands {
			if m.Content == cmd {
				log.Println(color.HiYellowString("Message handling skipped due to use of skip command."))
				return
			}
		}
	}

	// Process Files
	downloadCount := 0
	files := getFileLinks(m)
	for _, file := range files {
		log.Println(color.CyanString("> FILE: " + file.Link))

		status := startDownload(
			file.Link,
			file.Filename,
			channelConfig.Destination,
			m,
			file.Time,
			false,
		)
		if status.Status == downloadSuccess {
			downloadCount++
		}
	}

	// Save All Links to File
	if channelConfig.SaveAllLinksToFile != nil {
		filepath := *channelConfig.SaveAllLinksToFile
		if filepath != "" {
			f, err := os.OpenFile(filepath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
			if err != nil {
				log.Println(color.RedString("[SaveAllLinksToFile] Failed to open file:\t%s", err))
				f.Close()
				return
			}
			defer f.Close()

			var addedContent string
			rawLinks := getRawLinks(m)
			for _, rawLink := range rawLinks {
				addedContent = addedContent + "\n" + rawLink.Link
			}

			if _, err = f.WriteString(addedContent); err != nil {
				log.Println(color.RedString("[SaveAllLinksToFile] Failed to append file:\t%s", err))
				return
			}
		}
	}

	if downloadCount > 0 {
		// Filter Duplicate Images
		if config.FilterDuplicateImages {
			encodedStore, err := imgStore.GobEncode()
			if err != nil {
				log.Println(color.HiRedString("Failed to encode imgStore:\t%s"))
			} else {
				f, err := os.OpenFile(imgStorePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
				if err != nil {
					log.Println(color.HiRedString("Failed to open imgStore file:\t%s"))
				}
				_, err = f.Write(encodedStore)
				if err != nil {
					log.Println(color.HiRedString("Failed to update imgStore file:\t%s"))
				}
				err = f.Close()
				if err != nil {
					log.Println(color.HiRedString("Failed to close imgStore file:\t%s"))
				}
			}
		}
	}
}

var (
	historyCommandActive map[string]string
)

func handleHistory(commandingMessage *discordgo.Message, subjectChannelID string) int {
	historyCommandActive[subjectChannelID] = "downloading"

	var i int64 = 0
	var d int64 = 0
	var batch int = 0

	var err error
	var message *discordgo.Message = nil
	var commander string = "AUTORUN"
	if commandingMessage != nil {
		commander = getUserIdentifier(*commandingMessage.Author)
	}

	if isChannelRegistered(subjectChannelID) {
		channelConfig := getChannelConfig(subjectChannelID)

		historyStartTime := time.Now()

		if commandingMessage != nil {
			message, err = replyEmbed(commandingMessage, "Command — History", "Starting to save channel history, please wait...")
			if err != nil {
				log.Println(logPrefixHistory, color.HiRedString("%s/%s: Failed to send command embed message:\t%s",
					subjectChannelID, commander, err))
			}
		}
		log.Println(logPrefixHistory, color.CyanString("%s/%s: Began checking history...",
			subjectChannelID, commander))

		lastBefore := ""
		var lastBeforeTime time.Time
	MessageRequestingLoop:
		for true {
			if lastBeforeTime != (time.Time{}) {
				batch++
				if commandingMessage != nil {
					log.Println(logPrefixHistory, color.CyanString("%s/%s: Requesting 100 more, %d downloaded, %d processed — Before %s",
						subjectChannelID, commander, d, i, lastBeforeTime))
				}
				// Status update
				if commandingMessage != nil {
					if message != nil {
						content := fmt.Sprintf("``%s:`` **%s files downloaded**\n``%s messages processed``\n\n`(%d)` _Processing more messages, please wait..._",
							durafmt.ParseShort(time.Since(historyStartTime)).String(),
							formatNumber(d), formatNumber(i), batch)
						message, err = bot.ChannelMessageEditComplex(&discordgo.MessageEdit{
							ID:      message.ID,
							Channel: message.ChannelID,
							Embed:   buildEmbed(message.ChannelID, "Command — History", content),
						})
						// Edit failure
						if err != nil {
							log.Println(logPrefixHistory, color.RedString("%s/%s: Failed to edit status message, sending new one:\t%s", subjectChannelID, commander, err))
							message, err = replyEmbed(message, "Command — History", content)
							if err != nil {
								log.Println(logPrefixHistory, color.HiRedString("%s/%s: Failed to send replacement status message:\t%s", subjectChannelID, commander, err))
							}
						}
					} else {
						log.Println(logPrefixHistory, color.HiRedString("%s/%s: Tried to edit status message but it doesn't exist.", subjectChannelID, commander))
					}
				}
				// Update presence
				timeLastUpdated = time.Now()
				if *channelConfig.UpdatePresence {
					updateDiscordPresence()
				}
			}
			messages, err := bot.ChannelMessages(subjectChannelID, 100, lastBefore, "", "")
			if err == nil {
				if len(messages) <= 0 {
					delete(historyCommandActive, subjectChannelID)
					break MessageRequestingLoop
				}
				lastBefore = messages[len(messages)-1].ID
				lastBeforeTime, err = messages[len(messages)-1].Timestamp.Parse()
				if err != nil {
					log.Println(logPrefixHistory, color.RedString("%s/%s: Failed to fetch message timestamp:\t%s", subjectChannelID, commander, err))
				}
				for _, message := range messages {
					fileTime := time.Now()
					if message.Timestamp != "" {
						fileTime, err = message.Timestamp.Parse()
						if err != nil {
							log.Println(logPrefixHistory, color.RedString("%s/%s: Failed to parse message timestamp:\t%s", subjectChannelID, commander, err))
						}
					}
					if historyCommandActive[message.ChannelID] == "cancel" {
						delete(historyCommandActive, message.ChannelID)
						break MessageRequestingLoop
					}

					if message.Author.ID != user.ID || config.ScanOwnMessages {
						for _, iAttachment := range message.Attachments {
							if len(dbFindDownloadByURL(iAttachment.URL)) == 0 {
								download := startDownload(
									iAttachment.URL,
									iAttachment.Filename,
									channelConfig.Destination,
									message,
									fileTime,
									true,
								)
								if download.Status == downloadSuccess {
									d++
								}
							}
						}
						foundUrls := xurls.Strict().FindAllString(message.Content, -1)
						for _, iFoundUrl := range foundUrls {
							links := getDownloadLinks(iFoundUrl, subjectChannelID)
							for link, filename := range links {
								if len(dbFindDownloadByURL(link)) == 0 {
									download := startDownload(
										link,
										filename,
										channelConfig.Destination,
										message,
										fileTime,
										true,
									)
									if download.Status == downloadSuccess {
										d++
									}
								}
							}
						}
						i++
					}
				}
			} else {
				// Error requesting messages
				if message != nil {
					_, err = replyEmbed(message, "Command — History", fmt.Sprintf("Encountered an error requesting messages: %s", err.Error()))
					if err != nil {
						log.Println(logPrefixHistory, color.HiRedString("%s/%s: Failed to send error message:\t%s", subjectChannelID, commander, err))
					}
				}
				log.Println(logPrefixHistory, color.HiRedString("%s/%s: Error requesting messages:\t%s", subjectChannelID, commander, err))
				delete(historyCommandActive, subjectChannelID)
				break MessageRequestingLoop
			}
		}

		// Final status update
		if commandingMessage != nil {
			if message != nil {
				contentFinal := fmt.Sprintf("``%s:`` **%s total files downloaded!**\n``%s total messages processed``\n\nFinished cataloging history for ``%s``\n``%d`` message history requests\n\n_Duration was %s_",
					durafmt.ParseShort(time.Since(historyStartTime)).String(),
					formatNumber(int64(d)), formatNumber(int64(i)),
					subjectChannelID, batch,
					durafmt.Parse(time.Since(historyStartTime)).String(),
				)
				message, err = bot.ChannelMessageEditComplex(&discordgo.MessageEdit{
					ID:      message.ID,
					Channel: message.ChannelID,
					Embed:   buildEmbed(message.ChannelID, "Command — History", contentFinal),
				})
				// Edit failure
				if err != nil {
					log.Println(logPrefixHistory, color.RedString("%s/%s: Failed to edit status message, sending new one:\t%s",
						subjectChannelID, commander, err))
					message, err = replyEmbed(message, "Command — History", contentFinal)
					if err != nil {
						log.Println(logPrefixHistory, color.HiRedString("%s/%s: Failed to send replacement status message:\t%s",
							subjectChannelID, commander, err))
					}
				}
			} else {
				log.Println(logPrefixHistory, color.HiRedString("%s/%s: Tried to edit status message but it doesn't exist.", subjectChannelID, commander))
			}
		}

		// Final log
		log.Println(logPrefixHistory, color.HiCyanString("%s/%s: Finished history, %s files",
			subjectChannelID, commander, formatNumber(d)),
		)
	}

	return int(d)
}
