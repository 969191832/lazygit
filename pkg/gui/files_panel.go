package gui

import (

	// "io"
	// "io/ioutil"

	// "strings"

	"fmt"
	"regexp"
	"strings"

	"github.com/jesseduffield/gocui"
	"github.com/jesseduffield/lazygit/pkg/commands"
	"github.com/jesseduffield/lazygit/pkg/gui/presentation"
	"github.com/jesseduffield/lazygit/pkg/utils"
)

// list panel functions

func (gui *Gui) getSelectedFile() (*commands.File, error) {
	selectedLine := gui.State.Panels.Files.SelectedLine
	if selectedLine == -1 {
		return &commands.File{}, gui.Errors.ErrNoFiles
	}

	return gui.State.Files[selectedLine], nil
}

func (gui *Gui) selectFile(alreadySelected bool) error {
	gui.getFilesView().FocusPoint(0, gui.State.Panels.Files.SelectedLine)

	if gui.inDiffMode() {
		return gui.renderDiff()
	}

	file, err := gui.getSelectedFile()
	if err != nil {
		if err != gui.Errors.ErrNoFiles {
			return err
		}
		gui.State.SplitMainPanel = false
		gui.getMainView().Title = ""
		return gui.newStringTask("main", gui.Tr.SLocalize("NoChangedFiles"))
	}

	if !alreadySelected {
		if err := gui.resetOrigin(gui.getMainView()); err != nil {
			return err
		}
		if err := gui.resetOrigin(gui.getSecondaryView()); err != nil {
			return err
		}
	}

	if file.HasInlineMergeConflicts {
		gui.getMainView().Title = gui.Tr.SLocalize("MergeConflictsTitle")
		gui.State.SplitMainPanel = false
		return gui.refreshMergePanel()
	}

	if file.HasStagedChanges && file.HasUnstagedChanges {
		gui.State.SplitMainPanel = true
		gui.getMainView().Title = gui.Tr.SLocalize("UnstagedChanges")
		gui.getSecondaryView().Title = gui.Tr.SLocalize("StagedChanges")
		cmdStr := gui.GitCommand.DiffCmdStr(file, false, true)
		cmd := gui.OSCommand.ExecutableFromString(cmdStr)
		if err := gui.newPtyTask("secondary", cmd); err != nil {
			return err
		}
	} else {
		gui.State.SplitMainPanel = false
		if file.HasUnstagedChanges {
			gui.getMainView().Title = gui.Tr.SLocalize("UnstagedChanges")
		} else {
			gui.getMainView().Title = gui.Tr.SLocalize("StagedChanges")
		}
	}

	cmdStr := gui.GitCommand.DiffCmdStr(file, false, !file.HasUnstagedChanges && file.HasStagedChanges)
	cmd := gui.OSCommand.ExecutableFromString(cmdStr)
	if err := gui.newPtyTask("main", cmd); err != nil {
		return err
	}

	return nil
}

func (gui *Gui) refreshFiles() error {
	gui.State.RefreshingFilesMutex.Lock()
	gui.State.IsRefreshingFiles = true
	defer func() {
		gui.State.IsRefreshingFiles = false
		gui.State.RefreshingFilesMutex.Unlock()
	}()

	selectedFile, _ := gui.getSelectedFile()

	filesView := gui.getFilesView()
	if filesView == nil {
		// if the filesView hasn't been instantiated yet we just return
		return nil
	}
	if err := gui.refreshStateFiles(); err != nil {
		return err
	}

	gui.g.Update(func(g *gocui.Gui) error {
		displayStrings := presentation.GetFileListDisplayStrings(gui.State.Files, gui.State.Diff.Ref)
		gui.renderDisplayStrings(filesView, displayStrings)

		if g.CurrentView() == filesView || (g.CurrentView() == gui.getMainView() && g.CurrentView().Context == "merging") {
			newSelectedFile, _ := gui.getSelectedFile()
			alreadySelected := newSelectedFile.Name == selectedFile.Name
			return gui.selectFile(alreadySelected)
		}
		return nil
	})

	return nil
}

// specific functions

func (gui *Gui) stagedFiles() []*commands.File {
	files := gui.State.Files
	result := make([]*commands.File, 0)
	for _, file := range files {
		if file.HasStagedChanges {
			result = append(result, file)
		}
	}
	return result
}

func (gui *Gui) trackedFiles() []*commands.File {
	files := gui.State.Files
	result := make([]*commands.File, 0, len(files))
	for _, file := range files {
		if file.Tracked {
			result = append(result, file)
		}
	}
	return result
}

func (gui *Gui) stageSelectedFile(g *gocui.Gui) error {
	file, err := gui.getSelectedFile()
	if err != nil {
		return err
	}
	return gui.GitCommand.StageFile(file.Name)
}

func (gui *Gui) handleEnterFile(g *gocui.Gui, v *gocui.View) error {
	return gui.enterFile(false, -1)
}

func (gui *Gui) enterFile(forceSecondaryFocused bool, selectedLineIdx int) error {
	file, err := gui.getSelectedFile()
	if err != nil {
		if err != gui.Errors.ErrNoFiles {
			return err
		}
		return nil
	}
	if file.HasInlineMergeConflicts {
		return gui.handleSwitchToMerge()
	}
	if file.HasMergeConflicts {
		return gui.createErrorPanel(gui.Tr.SLocalize("FileStagingRequirements"))
	}
	gui.changeMainViewsContext("staging")
	if err := gui.switchFocus(gui.getFilesView(), gui.getMainView()); err != nil {
		return err
	}
	return gui.refreshStagingPanel(forceSecondaryFocused, selectedLineIdx)
}

func (gui *Gui) handleFilePress() error {
	file, err := gui.getSelectedFile()
	if err != nil {
		if err == gui.Errors.ErrNoFiles {
			return nil
		}
		return err
	}

	if file.HasInlineMergeConflicts {
		return gui.handleSwitchToMerge()
	}

	if file.HasUnstagedChanges {
		err = gui.GitCommand.StageFile(file.Name)
	} else {
		err = gui.GitCommand.UnStageFile(file.Name, file.Tracked)
	}
	if err != nil {
		return gui.surfaceError(err)
	}

	if err := gui.refreshSidePanels(refreshOptions{scope: []int{FILES}}); err != nil {
		return err
	}

	return gui.selectFile(true)
}

func (gui *Gui) allFilesStaged() bool {
	for _, file := range gui.State.Files {
		if file.HasUnstagedChanges {
			return false
		}
	}
	return true
}

func (gui *Gui) focusAndSelectFile() error {
	if _, err := gui.g.SetCurrentView("files"); err != nil {
		return err
	}

	return gui.selectFile(false)
}

func (gui *Gui) handleStageAll(g *gocui.Gui, v *gocui.View) error {
	var err error
	if gui.allFilesStaged() {
		err = gui.GitCommand.UnstageAll()
	} else {
		err = gui.GitCommand.StageAll()
	}
	if err != nil {
		_ = gui.surfaceError(err)
	}

	if err := gui.refreshSidePanels(refreshOptions{scope: []int{FILES}}); err != nil {
		return err
	}

	return gui.selectFile(false)
}

func (gui *Gui) handleIgnoreFile(g *gocui.Gui, v *gocui.View) error {
	file, err := gui.getSelectedFile()
	if err != nil {
		return gui.surfaceError(err)
	}

	if file.Tracked {
		return gui.ask(askOpts{
			returnToView:       gui.g.CurrentView(),
			returnFocusOnClose: true,
			title:              gui.Tr.SLocalize("IgnoreTracked"),
			prompt:             gui.Tr.SLocalize("IgnoreTrackedPrompt"),
			handleConfirm: func() error {
				if err := gui.GitCommand.Ignore(file.Name); err != nil {
					return err
				}
				if err := gui.GitCommand.RemoveTrackedFiles(file.Name); err != nil {
					return err
				}
				return gui.refreshSidePanels(refreshOptions{scope: []int{FILES}})
			},
		})
	}

	if err := gui.GitCommand.Ignore(file.Name); err != nil {
		return gui.surfaceError(err)
	}

	return gui.refreshSidePanels(refreshOptions{scope: []int{FILES}})
}

func (gui *Gui) handleWIPCommitPress(g *gocui.Gui, filesView *gocui.View) error {
	skipHookPreifx := gui.Config.GetUserConfig().GetString("git.skipHookPrefix")
	if skipHookPreifx == "" {
		return gui.createErrorPanel(gui.Tr.SLocalize("SkipHookPrefixNotConfigured"))
	}

	gui.renderStringSync("commitMessage", skipHookPreifx)
	if err := gui.getCommitMessageView().SetCursor(len(skipHookPreifx), 0); err != nil {
		return err
	}

	return gui.handleCommitPress()
}

func (gui *Gui) handleCommitPress() error {
	if len(gui.stagedFiles()) == 0 {
		return gui.promptToStageAllAndRetry(func() error {
			return gui.handleCommitPress()
		})
	}

	commitMessageView := gui.getCommitMessageView()
	prefixPattern := gui.Config.GetUserConfig().GetString("git.commitPrefixes." + utils.GetCurrentRepoName() + ".pattern")
	prefixReplace := gui.Config.GetUserConfig().GetString("git.commitPrefixes." + utils.GetCurrentRepoName() + ".replace")
	if len(prefixPattern) > 0 && len(prefixReplace) > 0 {
		rgx, err := regexp.Compile(prefixPattern)
		if err != nil {
			return gui.createErrorPanel(fmt.Sprintf("%s: %s", gui.Tr.SLocalize("commitPrefixPatternError"), err.Error()))
		}
		prefix := rgx.ReplaceAllString(gui.getCheckedOutBranch().Name, prefixReplace)
		gui.renderString("commitMessage", prefix)
		if err := commitMessageView.SetCursor(len(prefix), 0); err != nil {
			return err
		}
	}

	gui.g.Update(func(g *gocui.Gui) error {
		if _, err := g.SetViewOnTop("commitMessage"); err != nil {
			return err
		}

		if err := gui.switchFocus(gui.getFilesView(), commitMessageView); err != nil {
			return err
		}

		gui.RenderCommitLength()
		return nil
	})
	return nil
}

func (gui *Gui) promptToStageAllAndRetry(retry func() error) error {
	return gui.ask(askOpts{
		returnToView:       gui.getFilesView(),
		returnFocusOnClose: true,
		title:              gui.Tr.SLocalize("NoFilesStagedTitle"),
		prompt:             gui.Tr.SLocalize("NoFilesStagedPrompt"),
		handleConfirm: func() error {
			if err := gui.GitCommand.StageAll(); err != nil {
				return gui.surfaceError(err)
			}
			if err := gui.refreshFiles(); err != nil {
				return gui.surfaceError(err)
			}

			return retry()
		},
	})
}

func (gui *Gui) handleAmendCommitPress() error {
	if len(gui.stagedFiles()) == 0 {
		return gui.promptToStageAllAndRetry(func() error {
			return gui.handleAmendCommitPress()
		})
	}

	if len(gui.State.Commits) == 0 {
		return gui.createErrorPanel(gui.Tr.SLocalize("NoCommitToAmend"))
	}

	return gui.ask(askOpts{
		returnToView:       gui.getFilesView(),
		returnFocusOnClose: true,
		title:              strings.Title(gui.Tr.SLocalize("AmendLastCommit")),
		prompt:             gui.Tr.SLocalize("SureToAmend"),
		handleConfirm: func() error {
			ok, err := gui.runSyncOrAsyncCommand(gui.GitCommand.AmendHead())
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}

			return gui.refreshSidePanels(refreshOptions{mode: ASYNC})
		},
	})
}

// handleCommitEditorPress - handle when the user wants to commit changes via
// their editor rather than via the popup panel
func (gui *Gui) handleCommitEditorPress() error {
	if len(gui.stagedFiles()) == 0 {
		return gui.promptToStageAllAndRetry(func() error {
			return gui.handleCommitEditorPress()
		})
	}

	gui.PrepareSubProcess("git", "commit")
	return nil
}

// PrepareSubProcess - prepare a subprocess for execution and tell the gui to switch to it
func (gui *Gui) PrepareSubProcess(commands ...string) {
	gui.SubProcess = gui.GitCommand.PrepareCommitSubProcess()
	gui.g.Update(func(g *gocui.Gui) error {
		return gui.Errors.ErrSubProcess
	})
}

func (gui *Gui) editFile(filename string) error {
	_, err := gui.runSyncOrAsyncCommand(gui.OSCommand.EditFile(filename))
	return err
}

func (gui *Gui) handleFileEdit(g *gocui.Gui, v *gocui.View) error {
	file, err := gui.getSelectedFile()
	if err != nil {
		return gui.surfaceError(err)
	}

	return gui.editFile(file.Name)
}

func (gui *Gui) handleFileOpen(g *gocui.Gui, v *gocui.View) error {
	file, err := gui.getSelectedFile()
	if err != nil {
		return gui.surfaceError(err)
	}
	return gui.openFile(file.Name)
}

func (gui *Gui) handleRefreshFiles(g *gocui.Gui, v *gocui.View) error {
	return gui.refreshSidePanels(refreshOptions{scope: []int{FILES}})
}

func (gui *Gui) refreshStateFiles() error {
	// keep track of where the cursor is currently and the current file names
	// when we refresh, go looking for a matching name
	// move the cursor to there.
	selectedFile, _ := gui.getSelectedFile()

	// get files to stage
	files := gui.GitCommand.GetStatusFiles(commands.GetStatusFileOptions{})
	gui.State.Files = gui.GitCommand.MergeStatusFiles(gui.State.Files, files, selectedFile)

	if err := gui.fileWatcher.addFilesToFileWatcher(files); err != nil {
		return err
	}

	// let's try to find our file again and move the cursor to that
	for idx, f := range gui.State.Files {
		if selectedFile != nil && f.Matches(selectedFile) {
			gui.State.Panels.Files.SelectedLine = idx
			break
		}
	}

	gui.refreshSelectedLine(&gui.State.Panels.Files.SelectedLine, len(gui.State.Files))
	return nil
}

func (gui *Gui) handlePullFiles(g *gocui.Gui, v *gocui.View) error {
	// if we have no upstream branch we need to set that first
	currentBranch := gui.currentBranch()
	if currentBranch.Pullables == "?" {
		// see if we have this branch in our config with an upstream
		conf, err := gui.GitCommand.Repo.Config()
		if err != nil {
			return gui.surfaceError(err)
		}
		for branchName, branch := range conf.Branches {
			if branchName == currentBranch.Name {
				return gui.pullFiles(PullFilesOptions{RemoteName: branch.Remote, BranchName: branch.Name})
			}
		}

		return gui.prompt(v, gui.Tr.SLocalize("EnterUpstream"), "origin/"+currentBranch.Name, func(upstream string) error {
			if err := gui.GitCommand.SetUpstreamBranch(upstream); err != nil {
				errorMessage := err.Error()
				if strings.Contains(errorMessage, "does not exist") {
					errorMessage = fmt.Sprintf("upstream branch %s not found.\nIf you expect it to exist, you should fetch (with 'f').\nOtherwise, you should push (with 'shift+P')", upstream)
				}
				return gui.createErrorPanel(errorMessage)
			}
			return gui.pullFiles(PullFilesOptions{})
		})
	}

	return gui.pullFiles(PullFilesOptions{})
}

type PullFilesOptions struct {
	RemoteName string
	BranchName string
}

func (gui *Gui) pullFiles(opts PullFilesOptions) error {
	if err := gui.createLoaderPanel(gui.g.CurrentView(), gui.Tr.SLocalize("PullWait")); err != nil {
		return err
	}

	mode := gui.Config.GetUserConfig().GetString("git.pull.mode")

	go gui.pullWithMode(mode, opts)

	return nil
}

func (gui *Gui) pullWithMode(mode string, opts PullFilesOptions) error {
	err := gui.GitCommand.Fetch(
		commands.FetchOptions{
			PromptUserForCredential: gui.promptUserForCredential,
			RemoteName:              opts.RemoteName,
			BranchName:              opts.BranchName,
		},
	)
	gui.handleCredentialsPopup(err)
	if err != nil {
		return gui.refreshSidePanels(refreshOptions{mode: ASYNC})
	}

	switch mode {
	case "rebase":
		err := gui.GitCommand.RebaseBranch("FETCH_HEAD")
		return gui.handleGenericMergeCommandResult(err)
	case "merge":
		err := gui.GitCommand.Merge("FETCH_HEAD", commands.MergeOpts{})
		return gui.handleGenericMergeCommandResult(err)
	case "ff-only":
		err := gui.GitCommand.Merge("FETCH_HEAD", commands.MergeOpts{FastForwardOnly: true})
		return gui.handleGenericMergeCommandResult(err)
	default:
		return gui.createErrorPanel(fmt.Sprintf("git pull mode '%s' unrecognised", mode))
	}
}

func (gui *Gui) pushWithForceFlag(v *gocui.View, force bool, upstream string, args string) error {
	if err := gui.createLoaderPanel(v, gui.Tr.SLocalize("PushWait")); err != nil {
		return err
	}
	go func() {
		branchName := gui.getCheckedOutBranch().Name
		err := gui.GitCommand.Push(branchName, force, upstream, args, gui.promptUserForCredential)
		if err != nil && !force && strings.Contains(err.Error(), "Updates were rejected") {
			gui.ask(askOpts{
				returnToView:       v,
				returnFocusOnClose: true,
				title:              gui.Tr.SLocalize("ForcePush"),
				prompt:             gui.Tr.SLocalize("ForcePushPrompt"),
				handleConfirm: func() error {
					return gui.pushWithForceFlag(v, true, upstream, args)
				},
			})

			return
		}
		gui.handleCredentialsPopup(err)
		_ = gui.refreshSidePanels(refreshOptions{mode: ASYNC})
	}()
	return nil
}

func (gui *Gui) pushFiles(g *gocui.Gui, v *gocui.View) error {
	// if we have pullables we'll ask if the user wants to force push
	currentBranch := gui.currentBranch()

	if currentBranch.Pullables == "?" {
		// see if we have this branch in our config with an upstream
		conf, err := gui.GitCommand.Repo.Config()
		if err != nil {
			return gui.surfaceError(err)
		}
		for branchName, branch := range conf.Branches {
			if branchName == currentBranch.Name {
				return gui.pushWithForceFlag(v, false, "", fmt.Sprintf("%s %s", branch.Remote, branchName))
			}
		}

		if gui.GitCommand.PushToCurrent {
			return gui.pushWithForceFlag(v, false, "", "--set-upstream")
		} else {
			return gui.prompt(v, gui.Tr.SLocalize("EnterUpstream"), "origin "+currentBranch.Name, func(response string) error {
				return gui.pushWithForceFlag(v, false, response, "")
			})
		}
	} else if currentBranch.Pullables == "0" {
		return gui.pushWithForceFlag(v, false, "", "")
	}

	return gui.ask(askOpts{
		returnToView:       v,
		returnFocusOnClose: true,
		title:              gui.Tr.SLocalize("ForcePush"),
		prompt:             gui.Tr.SLocalize("ForcePushPrompt"),
		handleConfirm: func() error {
			return gui.pushWithForceFlag(v, true, "", "")
		},
	})
}

func (gui *Gui) handleSwitchToMerge() error {
	file, err := gui.getSelectedFile()
	if err != nil {
		if err != gui.Errors.ErrNoFiles {
			return gui.surfaceError(err)
		}
		return nil
	}
	if !file.HasInlineMergeConflicts {
		return gui.createErrorPanel(gui.Tr.SLocalize("FileNoMergeCons"))
	}
	gui.changeMainViewsContext("merging")
	if err := gui.switchFocus(gui.g.CurrentView(), gui.getMainView()); err != nil {
		return err
	}
	return gui.refreshMergePanel()
}

func (gui *Gui) openFile(filename string) error {
	if err := gui.OSCommand.OpenFile(filename); err != nil {
		return gui.surfaceError(err)
	}
	return nil
}

func (gui *Gui) anyFilesWithMergeConflicts() bool {
	for _, file := range gui.State.Files {
		if file.HasMergeConflicts {
			return true
		}
	}
	return false
}

func (gui *Gui) handleCustomCommand(g *gocui.Gui, v *gocui.View) error {
	return gui.prompt(v, gui.Tr.SLocalize("CustomCommand"), "", func(command string) error {
		gui.SubProcess = gui.OSCommand.RunCustomCommand(command)
		return gui.Errors.ErrSubProcess
	})
}

func (gui *Gui) handleCreateStashMenu(g *gocui.Gui, v *gocui.View) error {
	menuItems := []*menuItem{
		{
			displayString: gui.Tr.SLocalize("stashAllChanges"),
			onPress: func() error {
				return gui.handleStashSave(gui.GitCommand.StashSave)
			},
		},
		{
			displayString: gui.Tr.SLocalize("stashStagedChanges"),
			onPress: func() error {
				return gui.handleStashSave(gui.GitCommand.StashSaveStagedChanges)
			},
		},
	}

	return gui.createMenu(gui.Tr.SLocalize("stashOptions"), menuItems, createMenuOptions{showCancel: true})
}

func (gui *Gui) handleStashChanges(g *gocui.Gui, v *gocui.View) error {
	return gui.handleStashSave(gui.GitCommand.StashSave)
}

func (gui *Gui) handleCreateResetToUpstreamMenu(g *gocui.Gui, v *gocui.View) error {
	return gui.createResetMenu("@{upstream}")
}

func (gui *Gui) onFilesPanelSearchSelect(selectedLine int) error {
	gui.State.Panels.Files.SelectedLine = selectedLine
	return gui.focusAndSelectFile()
}
