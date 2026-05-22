/*
    TODO: We want to port the moebius ANSI editor hosted here: https://github.com/blocktronics/moebius and extend it to work with Avatar Chat.

    There is already a sort of chat server embedded as well as a networked canvas - they might use the same port / service, we will probably want to decouple the networked canvas from the chat portion, the latter we will want to extend or swap out while the former is something that can be ignored or delegated to another server.

    So essentially we are trying to make an avatar chat / json-chat bridge inside of moebius.  The first thing is a protocol level chat replacement so someone in moebius chat can send and receive messages.

    As we develop further, we want the moebius chat to be able to send ANSI messages to a channel - this might be something just as simple as adding another menu-item to the File Menu which says "Send to Chat" or somesuch and uses the typical avatar chat encoding to share with consumers.  This feature is the added utility that the moebius editor brings to the table, graphic fidelity.

    Once we get the above working, we'll probably have to look into changing how the moebius chat works.  We want to be able to render things in a rich context, not the stale moebius chat.  We will probably wind up swapping a lot of it out.  We want to show avatars and art to the degree it is possible, with some nice colorized styling.  We also need a way to set our avatar in the program eventually. 
*/