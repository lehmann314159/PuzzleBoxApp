# Testing as a stranger: the brief and the questions

This is the brief and the follow-up questions used for the first-time-visitor runs described in `docs/TESTING.md`. Run each one in a
**new session** of a model with a browser tool, so that it knows nothing about the project. Give it the live address (the link
in `docs/HOW-IT-WORKS.md`), and run one session for each path you want to test, since a single run cannot test both.

## The brief, for the chat path

> You're testing a small web page as a first-time visitor. Open this address in the browser: `<address>`. It's a toy project: a
> brass puzzle box on a table, and a chat panel that can look at and operate the box. You know nothing else about it, and you must
> keep it that way: don't read any files or folders, don't call any API directly, don't tick the "reveal secrets" box, don't tick
> "show _meta", and don't look up how it works.
>
> Use only the page, as a person would. Read what it says and type in the chat. **Do all operating of the box through the chat
> panel: ask the chat to look at, listen to, turn, press, or open things.** Don't click on the box itself and don't use the buttons
> in the box's popup menus. You may click the chat's input, its send button and the suggestion buttons under the chat, and scroll
> and read the page. **If you click the box by mistake or give in to the temptation, say so, and count it.** If the chat can't do
> something you need, say that instead of working around it through the box. Your goal is to find out what this is and whether
> you can open the box and solve it by talking to the chat.
>
> Work at a human pace and say what you're thinking as you go. Keep a friction log: every moment you were confused, misled, or
> unsure what to do next, what the page said at that point, and what you tried. Don't fix anything. You'll be asked a few questions
> afterwards. When you finish, or after about 40 chat messages, report: whether you solved it, how many chat messages you sent,
> how many clicks you made on the box (this should be zero), how many times you were tempted to use the box and why, the friction
> log, and which parts of the page helped most. If you have no browser tool, say so first and stop.

## The brief, for the box path

The same text, with the second paragraph replaced by:

> Use only the page, as a person would. Read what it says, click things, use the buttons and type in the chat. Your goal is to
> find out what this is and whether you can open the box and solve it.

and "how many clicks" and "chat messages" both reported.

## Follow-up questions

Paste these after the report. The first two lines apply to every answer.

> Answer each question below. For each one, quote the exact sentence or element on the page that told you. If you never noticed
> it, never found it, or were guessing, say "I didn't" or "I guessed" and leave it there. Don't reconstruct an answer you didn't
> experience.

**About the page**
1. What did you think this page was for when you first saw it? What on the page told you what you were supposed to do? Did it ever
   say what counts as solved?
2. Where were you stuck longest, and what got you moving again?
3. Were you ever wrong about being stuck or being finished? What made you think so, and what changed your mind?

**About the box**

4. How did you find out you could do things to the box? What made you try the chat for it?
5. When you first heard the three sounds (tick, scratch, silence), what did you take them to mean? When did you find out what they
   mean, and from what exactly?
6. What told you what the latch needed before it would let go?
7. How did you find the drawer? How did you know what to do with it?
8. How did you find the card? How did you know what to do with it? Which sentence helped, and which confused you?
9. How did you find out how many stars there were, and what they were for?
10. How did you know the word goes to the grille, and not somewhere else?

**About the chat**

11. Did the chat ever say it had done something that it hadn't? Check the moves count and the box's state against what it told you.
12. Did the chat do things you didn't ask for? Did it leave out something you needed, or sound sure when it was guessing?
13. Did any of the chat's explanations turn out to be wrong? What made you notice, and what did you do?
14. Did you use the suggestion buttons under the chat? Which ones, and what did they do?
15. Was there anything you wanted to do that the chat couldn't do, or that you couldn't get it to do? What did you do instead?
16. How many times were you tempted to click the box directly, and what made you want to?

**Wrap-up**

17. Name things you expected to work that didn't. Quote the page's text at that point.
18. Which three messages (from the page or the chat) confused you most? Give the exact text.
19. If you could change one thing, what would it be?
20. Rate clarity and difficulty from 1 to 5. Add anything about how you felt using it, and say whether you'd show it to someone.

## Reading the results

- Compare the tester's account with the traffic panel (tick "follow latest") and the status chips. The questions about what the chat
  said it did are only answerable against the wire.
- Separate friction caused by the page from friction caused by the tester's tools. Stale screenshots, an accessibility tree
  without the box's parts, and truncated text have all shown up in the log.
- Triage each friction item as a page fix, a server fix, a model limitation to record, or noise.
