Feature: Settings can be searched
  The Settings drawer has grown past what one can scan: eighteen tabs, two of them
  lists whose fields exist only inside an open row, and a folded block in the
  provider form. One field searches every tab by the names, config keys and
  descriptions the page draws, and a result takes the operator to the setting.

  Scenario: Slash puts the cursor in the search
    Then slash puts the cursor in the settings search unless a field has it or a dialog is open

  Scenario: A query shows matching settings from every tab
    Then typing a query shows the matching settings of every tab in place of the sections, with the words marked

  Scenario: A result opens its tab and the setting
    Then choosing a field in another tab opens that tab, rings the setting and puts the cursor in it
    And searching and choosing a result leave unsaved edits as they were

  Scenario: A field of a provider row opens that row
    Then choosing a field of a provider row opens that row and unfolds the block it stands in
    And choosing a field of another row of the open list reveals it in that row, not in the one that was open

  Scenario: A mouse gets the setting's control, a touch screen its box
    Then on a fine pointer the focus goes to the control named by the primary name (Proxy URL's input, a model's id input), never a switch beside it, a disabled input or the (i)
    And on a touch-only device the focus goes to the setting's named box, so no keyboard opens

  Scenario: The query stays for the next result
    Then the query stays after a choice, and the field brings the results back with the chosen one highlighted

  Scenario: Escape and Telegram's Back clear the search first
    Then Escape with results showing clears the query wherever the focus is
    And the Telegram back button clears shown results first, then goes back from a tab to the tiles

  Scenario: Russian names and config keys both find a setting
    Then a Russian page finds a setting by its Russian name and by its config key

  Scenario: A relay's own settings are searched
    Then a relay's settings are searched too
