import AppKit

/// Builds the menu bar. Items that drive the web UI carry their bridge command
/// in representedObject and send `runCommand(_:)` up the responder chain to
/// the app delegate; standard items use AppKit's own selectors so text editing
/// shortcuts work inside the web view.
struct PanelItem {
    let title: String
    let id: String
    let key: String
    var mods: NSEvent.ModifierFlags = .command
}

@MainActor
struct MainMenu {
    let menu = NSMenu()
    let interceptRequests: NSMenuItem
    let interceptResponses: NSMenuItem
    let inspector: NSMenuItem

    /// Rail order. ⌘1–⌘9 go to the first nine; Activity takes ⌥⌘9. Settings
    /// is ⌘, in the app menu (no second shortcut). None of these collide with
    /// a standard macOS shortcut or a Mod+ combo in the web UI (keys.js).
    static let panels: [PanelItem] = [
        PanelItem(title: "Proxy", id: "proxy", key: "1"),
        PanelItem(title: "Intercept", id: "intercept", key: "2"),
        PanelItem(title: "Repeater", id: "repeater", key: "3"),
        PanelItem(title: "Intruder", id: "intruder", key: "4"),
        PanelItem(title: "Collections", id: "collections", key: "5"),
        PanelItem(title: "Scanner", id: "scanner", key: "6"),
        PanelItem(title: "Map", id: "map", key: "7"),
        PanelItem(title: "Findings", id: "findings", key: "8"),
        PanelItem(title: "Notes", id: "notes", key: "9"),
        PanelItem(title: "Activity", id: "activity", key: "9", mods: [.command, .option]),
        PanelItem(title: "Settings", id: "settings", key: ""),
    ]

    init() {
        interceptRequests = Self.command("Intercept Requests", "intercept:toggle-request", "i")
        interceptResponses = Self.command("Intercept Responses", "intercept:toggle-response", "I")
        inspector = NSMenuItem(title: "Web Inspector", action: #selector(AppDelegate.toggleInspector(_:)), keyEquivalent: "i")
        inspector.keyEquivalentModifierMask = [.command, .option]

        menu.addItem(submenu(appMenu()))
        menu.addItem(submenu(fileMenu()))
        menu.addItem(submenu(editMenu()))
        menu.addItem(submenu(viewMenu()))
        menu.addItem(submenu(proxyMenu()))
        let window = windowMenu()
        menu.addItem(submenu(window))
        let help = helpMenu()
        menu.addItem(submenu(help))
        NSApp.windowsMenu = window
        NSApp.helpMenu = help
    }

    static func command(_ title: String, _ command: String, _ key: String = "",
                        _ mods: NSEvent.ModifierFlags = .command) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: #selector(AppDelegate.runCommand(_:)), keyEquivalent: key)
        item.keyEquivalentModifierMask = mods
        item.representedObject = command
        return item
    }

    private static func item(_ title: String, _ action: Selector?, _ key: String = "",
                             _ mods: NSEvent.ModifierFlags = .command) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
        item.keyEquivalentModifierMask = mods
        return item
    }

    private func submenu(_ m: NSMenu) -> NSMenuItem {
        let holder = NSMenuItem(title: m.title, action: nil, keyEquivalent: "")
        holder.submenu = m
        return holder
    }

    private func appMenu() -> NSMenu {
        let m = NSMenu(title: "Interseptor")
        m.addItem(Self.item("About Interseptor", #selector(NSApplication.orderFrontStandardAboutPanel(_:))))
        m.addItem(.separator())
        m.addItem(Self.command("Settings…", "panel:settings", ","))
        m.addItem(.separator())
        let services = NSMenuItem(title: "Services", action: nil, keyEquivalent: "")
        let servicesMenu = NSMenu(title: "Services")
        services.submenu = servicesMenu
        NSApp.servicesMenu = servicesMenu
        m.addItem(services)
        m.addItem(.separator())
        m.addItem(Self.item("Hide Interseptor", #selector(NSApplication.hide(_:)), "h"))
        m.addItem(Self.item("Hide Others", #selector(NSApplication.hideOtherApplications(_:)), "h", [.command, .option]))
        m.addItem(Self.item("Show All", #selector(NSApplication.unhideAllApplications(_:))))
        m.addItem(.separator())
        m.addItem(Self.item("Quit Interseptor", #selector(NSApplication.terminate(_:)), "q"))
        return m
    }

    private func fileMenu() -> NSMenu {
        let m = NSMenu(title: "File")
        m.addItem(Self.command("Switch Project…", "project:switch", "o"))
        m.addItem(Self.item("Connect to Server…", #selector(AppDelegate.connectToServer(_:))))
        m.addItem(.separator())
        m.addItem(Self.item("Close Window", #selector(NSWindow.performClose(_:)), "w"))
        return m
    }

    private func editMenu() -> NSMenu {
        let m = NSMenu(title: "Edit")
        m.addItem(Self.item("Undo", Selector(("undo:")), "z"))
        m.addItem(Self.item("Redo", Selector(("redo:")), "z", [.command, .shift]))
        m.addItem(.separator())
        m.addItem(Self.item("Cut", #selector(NSText.cut(_:)), "x"))
        m.addItem(Self.item("Copy", #selector(NSText.copy(_:)), "c"))
        m.addItem(Self.item("Paste", #selector(NSText.paste(_:)), "v"))
        m.addItem(Self.item("Paste and Match Style", #selector(NSTextView.pasteAsPlainText(_:)), "v", [.command, .option, .shift]))
        m.addItem(Self.item("Delete", #selector(NSText.delete(_:))))
        m.addItem(Self.item("Select All", #selector(NSText.selectAll(_:)), "a"))
        m.addItem(.separator())
        m.addItem(Self.command("Find…", "find", "f"))
        return m
    }

    private func viewMenu() -> NSMenu {
        let m = NSMenu(title: "View")
        m.addItem(Self.command("Command Palette", "palette:open", "k"))
        m.addItem(.separator())
        for p in Self.panels {
            m.addItem(Self.command(p.title, "panel:\(p.id)", p.key, p.mods))
        }
        m.addItem(.separator())
        m.addItem(Self.command("Toggle Theme", "theme:toggle"))
        m.addItem(Self.item("Reload", #selector(AppDelegate.reloadPage(_:)), "r", [.command, .option]))
        m.addItem(.separator())
        m.addItem(Self.item("Enter Full Screen", #selector(NSWindow.toggleFullScreen(_:)), "f", [.command, .control]))
        return m
    }

    private func proxyMenu() -> NSMenu {
        let m = NSMenu(title: "Proxy")
        m.addItem(interceptRequests)
        m.addItem(interceptResponses)
        return m
    }

    private func windowMenu() -> NSMenu {
        let m = NSMenu(title: "Window")
        m.addItem(Self.item("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m"))
        m.addItem(Self.item("Zoom", #selector(NSWindow.performZoom(_:))))
        m.addItem(.separator())
        m.addItem(Self.item("Interseptor", #selector(AppDelegate.showMainWindow(_:)), "0"))
        m.addItem(.separator())
        m.addItem(Self.item("Bring All to Front", #selector(NSApplication.arrangeInFront(_:))))
        return m
    }

    private func helpMenu() -> NSMenu {
        let m = NSMenu(title: "Help")
        m.addItem(Self.item("Interseptor Help", #selector(AppDelegate.openHelp(_:))))
        m.addItem(Self.item("Show Log", #selector(AppDelegate.showLog(_:))))
        m.addItem(.separator())
        let dev = NSMenu(title: "Developer")
        dev.addItem(inspector)
        let devItem = NSMenuItem(title: "Developer", action: nil, keyEquivalent: "")
        devItem.submenu = dev
        m.addItem(devItem)
        return m
    }
}
