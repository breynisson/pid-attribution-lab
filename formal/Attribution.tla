--------------------------- MODULE Attribution ---------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS Scopes, PIDs, Incarnations, Workloads, Events, None,
          Faulty, AllowReplay

Keys == [scope : Scopes, pid : PIDs, incarnation : Incarnations]
Metadata == [key : Keys, workload : Workloads]
MetaMessage(k, w) == [kind |-> "metadata", key |-> k, workload |-> w]
ExitMessage(k) == [kind |-> "exit", key |-> k]
ActivityMessage(e, k) == [kind |-> "activity", event |-> e, key |-> k]
Messages ==
    {MetaMessage(k, w) : k \in Keys, w \in Workloads}
    \cup {ExitMessage(k) : k \in Keys}
    \cup {ActivityMessage(e, k) : e \in Events, k \in Keys}

VARIABLES started, live, worldMetadata, oracle,
          sent, pending, seenMetadata,
          metadata, received, results

vars == <<started, live, worldMetadata, oracle, sent, pending,
          seenMetadata, metadata, received, results>>

\* World/oracle and seenMetadata are harness state. Backend actions never read
\* them to choose an attribution. sent/pending model an unordered transport.
Init ==
    /\ started = {}
    /\ live = {}
    /\ worldMetadata = [k \in Keys |-> None]
    /\ oracle = [e \in Events |-> None]
    /\ sent = {}
    /\ pending = {}
    /\ seenMetadata = {}
    /\ metadata = {}
    /\ received = [e \in Events |-> None]
    /\ results = [e \in Events |-> None]

Start(k, w) ==
    /\ k \notin started
    /\ ~\E old \in live : old.scope = k.scope /\ old.pid = k.pid
    /\ ~\E old \in started :
           old.scope = k.scope /\ old.incarnation = k.incarnation
    /\ started' = started \cup {k}
    /\ live' = live \cup {k}
    /\ worldMetadata' = [worldMetadata EXCEPT ![k] = w]
    /\ sent' = sent \cup {MetaMessage(k, w)}
    /\ pending' = pending \cup {MetaMessage(k, w)}
    /\ UNCHANGED <<oracle, seenMetadata, metadata, received, results>>

Capture(k, e) ==
    /\ k \in live
    /\ oracle[e] = None
    \* Truth is copied from the physical world at capture, not from the backend.
    /\ oracle' = [oracle EXCEPT
                      ![e] = [key |-> k, workload |-> worldMetadata[k]]]
    /\ sent' = sent \cup {ActivityMessage(e, k)}
    /\ pending' = pending \cup {ActivityMessage(e, k)}
    /\ UNCHANGED <<started, live, worldMetadata, seenMetadata,
                   metadata, received, results>>

Exit(k) ==
    /\ k \in live
    /\ live' = live \ {k}
    /\ sent' = sent \cup {ExitMessage(k)}
    /\ pending' = pending \cup {ExitMessage(k)}
    /\ UNCHANGED <<started, worldMetadata, oracle, seenMetadata,
                   metadata, received, results>>

\* The only backend inputs are the delivered message m and backend state.
\* A record in metadata is delivered data, not worldMetadata or oracle.
Deliver(m) ==
    /\ m \in pending
    /\ pending' = pending \ {m}
    /\ metadata' =
        CASE m.kind = "metadata" ->
            (IF Faulty THEN {d \in metadata : d.key.pid # m.key.pid}
                       ELSE metadata)
            \cup {[key |-> m.key, workload |-> m.workload]}
        [] m.kind = "exit" ->
            IF Faulty THEN {d \in metadata : d.key.pid # m.key.pid}
                      ELSE metadata
        [] OTHER -> metadata
    /\ received' =
        IF m.kind = "activity"
        THEN IF ~Faulty /\ received[m.event] # None
             THEN received
             ELSE [received EXCEPT ![m.event] = m.key]
        ELSE received
    /\ results' =
        CASE m.kind = "activity" ->
            IF ~Faulty /\ received[m.event] # None
            THEN results
            ELSE LET matches ==
                         {d \in metadata :
                             IF Faulty THEN d.key.pid = m.key.pid
                                       ELSE d.key = m.key}
                 IN [results EXCEPT
                        ![m.event] = IF matches = {} THEN None
                                    ELSE CHOOSE d \in matches : TRUE]
        [] m.kind = "metadata" /\ ~Faulty ->
            [e \in Events |->
                IF received[e] = m.key /\ results[e] = None
                THEN [key |-> m.key, workload |-> m.workload]
                ELSE results[e]]
        [] OTHER -> results
    \* Audit which metadata has ever arrived, even when faulty deletes it.
    /\ seenMetadata' =
        IF m.kind = "metadata"
        THEN seenMetadata \cup {[key |-> m.key, workload |-> m.workload]}
        ELSE seenMetadata
    /\ UNCHANGED <<started, live, worldMetadata, oracle, sent>>

\* Nonconflicting duplicates can be delivered arbitrarily often. A pending set
\* coalesces simultaneous identical copies; replay after delivery models them.
Replay(m) ==
    /\ AllowReplay
    /\ m \in sent \ pending
    /\ pending' = pending \cup {m}
    /\ UNCHANGED <<started, live, worldMetadata, oracle, sent, seenMetadata,
                   metadata, received, results>>

Next ==
    \/ \E k \in Keys, w \in Workloads : Start(k, w)
    \/ \E k \in Keys, e \in Events : Capture(k, e)
    \/ \E k \in Keys : Exit(k)
    \/ \E m \in Messages : Deliver(m)
    \/ \E m \in Messages : Replay(m)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ started \subseteq Keys
    /\ live \subseteq started
    /\ \A a, b \in live : (a.scope = b.scope /\ a.pid = b.pid) => a = b
    /\ worldMetadata \in [Keys -> Workloads \cup {None}]
    /\ oracle \in [Events -> Metadata \cup {None}]
    /\ sent \subseteq Messages
    /\ pending \subseteq sent
    /\ seenMetadata \subseteq Metadata
    /\ metadata \subseteq seenMetadata
    /\ received \in [Events -> Keys \cup {None}]
    /\ results \in [Events -> Metadata \cup {None}]

NoFalseAttribution ==
    \A e \in Events : results[e] # None => results[e] = oracle[e]

UsefulAttribution ==
    \A e \in Events :
        (received[e] # None /\ \E d \in seenMetadata : d.key = received[e])
        => results[e] # None

HonestUncertainty ==
    \A e \in Events :
        results[e] # None =>
            \E d \in seenMetadata : d = results[e] /\ d.key = received[e]

\* No world progress is required: fairness applies only to deliveries that
\* remain enabled. Queued messages cannot be dropped; backend history persists.
FairSpec == Spec /\ \A m \in Messages : WF_vars(Deliver(m))
EventualResolution ==
    \A e \in Events : (oracle[e] # None) ~> (results[e] # None)
=============================================================================
