package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/fatih/color"
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

//#region Events

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	handleMessage(m.Message, false, false)
}

func messageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if m.EditedTimestamp != discordgo.Timestamp("") {
		handleMessage(m.Message, true, false)
	}
}

func handleMessage(m *discordgo.Message, edited bool, history bool) int64 {
	if !isChannelRegistered(m.ChannelID) {
		return -1
	}
	channelConfig := getChannelConfig(m.ChannelID)

	// Ignore own messages unless told not to
	if m.Author.ID == user.ID && !config.ScanOwnMessages {
		return -1
	}
	// Ignore bots if told to do so
	if m.Author.Bot && *channelConfig.IgnoreBots {
		return -1
	}
	// Ignore if told so by config
	if !*channelConfig.Enabled || (edited && !*channelConfig.ScanEdits) {
		return -1
	}

	// If message content is empty (likely due to userbot/selfbot)
	if m.Content == "" && len(m.Attachments) == 0 {
		nms, err := bot.ChannelMessages(m.ChannelID, 10, "", "", "")
		if err == nil {
			if len(nms) > 0 {
				for _, nm := range nms {
					if nm.ID == m.ID {
						m = nm
						dgr.FindAndExecute(bot, strings.ToLower(config.CommandPrefix), bot.State.User.ID, messageToLower(m))
					}
				}
			}
		}
	}

	// Log
	var sendLabel string
	if config.DebugOutput {
		sendLabel = fmt.Sprintf("%s/%s/%s", m.GuildID, m.ChannelID, m.Author.ID)
	} else {
		sendLabel = fmt.Sprintf("%s in %s", getUserIdentifier(*m.Author), getSourceName(m.GuildID, m.ChannelID))
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
			return -1
		}
	}
	// User Blacklisting
	if channelConfig.UserBlacklist != nil {
		if stringInSlice(m.Author.ID, *channelConfig.UserBlacklist) {
			log.Println(color.HiYellowString("Message handling skipped due to user being blacklisted."))
			return -1
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
				return -1
			}
		}
	}

	// Process Files
	var downloadCount int64
	files := getFileLinks(m)
	for _, file := range files {
		log.Println(color.CyanString("> FILE: " + file.Link))

		status := startDownload(
			file.Link,
			file.Filename,
			channelConfig.Destination,
			m,
			file.Time,
			history,
		)
		if status.Status == downloadSuccess {
			downloadCount++
		}
	}
	return downloadCount
}

//#endregion
