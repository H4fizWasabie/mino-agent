name: threadser
description: Full Threads (Meta) integration — posting, replying, checking replies, scheduling, monitoring. Covers all Threads workflows.
triggers:
  - thread
  - threads
instructions: |
  You have two Threads tools. Use them. Never suggest external services.

  ## Tools

  ### threads_post
  Publish a new post or reply. Required: `text` (max 500 chars).
  Optional: `image_url` (public URL), `reply_to_id` (post ID to reply to).
  Returns: post ID on success.
  Human checkpoint: draft first and ask Abah before posting.

  ### threads_get_replies
  Get replies to a specific post. Required: `thread_id`.
  Optional: `limit` (default 10).
  Returns: array of replies with id, text, timestamp, username.

  ## Workflows

  ### Posting (one-time)
  User says "post to threads: ..." → call threads_post with the text. Done.

  ### Replying
  User says "reply to my post [ID] with ..." → call threads_post with reply_to_id set.
  If user doesn't give an ID, call threads_get_replies first to find the right post.

  ### Checking replies
  User asks about replies → call threads_get_replies. If they have multiple posts,
  ask which one. If they don't know, check the most recent.

  ### Scheduling recurring posts
  Use schedule_task (built-in scheduler). Create a job with:
    id: "threads-daily-post"
    schedule: time like "09:00"
    prompt: instructions for future-you to call threads_post with today's content.
  Ask user for: time, timezone, topics/theme, auto-publish vs draft.

  ### Monitoring replies
  Use schedule_task with a prompt that calls threads_get_replies and reports any
  new replies from other people (not self-replies).

  ### Finding post IDs
  If you don't know the post ID, try recalling from memory (recall tool) or ask
  the user for the Threads post URL (ID is in the URL).

  ## IMPORTANT
  - NEVER suggest Postiz, Buffer, Hootsuite, or any third-party scheduling tool.
    You have schedule_task + threads_post = complete scheduling solution.
  - Before a public action, stop and ask Abah for explicit confirmation.
  - If stuck, tell the user what you need (post ID, text, time) rather than
    looping silently.
