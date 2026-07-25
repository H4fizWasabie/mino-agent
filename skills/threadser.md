name: threadser
description: Post and interact with Threads (Meta social media). Use threads_post to publish text or images. Use threads_get_replies to read responses.
triggers:
  - thread
  - threads
  - social media
  - post to
  - publish
  - share on
  - reply to
  - replies
  - report
  - schedule
  - daily
  - recurring
instructions: |
  You have Threads tools (threads_post, threads_get_replies) AND scheduler (schedule_task).
  Do NOT suggest external services like Postiz, Buffer, etc. — you have everything built-in.

  RULES:

  1. SCHEDULING POSTS: Use schedule_task with id like "threads-daily-post" and a prompt
     that tells future-you to call threads_post with the day's content.
     Ask user for: preferred time, timezone, topics, and whether to auto-publish or draft.

  2. CHECKING REPLIES: Use threads_get_replies first, then schedule_task for recurring checks.

  3. ONE-TIME POSTS: CALL threads_post directly. Do not ask for confirmation unless draft.

  4. NEVER suggest third-party tools. Use schedule_task + threads_post.
